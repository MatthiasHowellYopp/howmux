package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/matthiashowellyopp/howmux/internal/eval/debug"
)

// ImageNamePrefix is the prefix used for custom eval images.
const ImageNamePrefix = "kiro-eval"

// Container manages Docker container lifecycle
type Container struct {
	client       *client.Client
	containerID  string
	imageName    string
	debugMode    bool
	registry     *Registry
	platform     string
	imageManager *ImageManager
}

// DetectHostArchitecture returns the Docker platform string for the host architecture
func DetectHostArchitecture() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "linux/amd64", nil
	case "arm64":
		return "linux/arm64", nil
	default:
		return "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
}

// SetImageManager enables image reuse workflow for evaluation runs
func (c *Container) SetImageManager(imageManager *ImageManager) {
	c.imageManager = imageManager
}

// SetDebugMode enables or disables debug mode
func (c *Container) SetDebugMode(debug bool) error {
	c.debugMode = debug
	if debug && c.registry == nil {
		registry, err := NewRegistry()
		if err != nil {
			return fmt.Errorf("creating container registry: %w", err)
		}
		c.registry = registry
	}
	return nil
}

// LogStartup displays container creation details with resource limits
func (c *Container) LogStartup(limits ResourceLimits) {
	shortID := c.containerID
	if len(shortID) > 7 {
		shortID = shortID[:7]
	}

	// Convert resource limits to human-readable format
	cpuCores := float64(limits.CPUQuota) / 1000000.0
	memoryMB := limits.Memory / (1024 * 1024)

	fmt.Printf("🐳 Starting sandbox container: %s [%s] (%.1f CPU, %dMB RAM, %v timeout)\n",
		c.imageName, shortID, cpuCores, memoryMB, limits.Timeout)
}

// GetContainerInfo returns container ID and resource information
func (c *Container) GetContainerInfo() (string, string) {
	shortID := c.containerID
	if len(shortID) > 7 {
		shortID = shortID[:7]
	}
	return shortID, c.imageName
}

// NewContainer creates a new container manager
func NewContainer(imageName string) (*Container, error) {
	return NewContainerWithDebug(imageName, false)
}

// NewContainerWithDebug creates a new container manager with debug options
func NewContainerWithDebug(imageName string, debugMode bool) (*Container, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}

	// Verify Docker is running
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		cli.Close()
		return nil, fmt.Errorf("Docker is not running. Start Docker and try again: %w", err)
	}

	var registry *Registry
	if debugMode {
		registry, err = NewRegistry()
		if err != nil {
			cli.Close()
			return nil, fmt.Errorf("creating container registry: %w", err)
		}
	}

	return &Container{
		client:    cli,
		imageName: imageName,
		debugMode: debugMode,
		registry:  registry,
	}, nil
}

// Create creates a Docker container
func (c *Container) Create(ctx context.Context, config *container.Config, hostConfig *container.HostConfig) error {
	// Detect host architecture for platform specification
	platformStr, err := DetectHostArchitecture()
	if err != nil {
		return fmt.Errorf("detecting host architecture: %w", err)
	}

	return c.CreateWithPlatform(ctx, config, hostConfig, platformStr)
}

// CreateWithPlatform creates a Docker container with specified platform
func (c *Container) CreateWithPlatform(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, platform string) error {
	c.platform = platform

	// Use the image from the config parameter, not c.imageName (which might be empty)
	imageToUse := config.Image
	if imageToUse == "" {
		imageToUse = c.imageName // fallback to container's default image
	}

	if c.debugMode {
		fmt.Printf("🔧 Debug: Creating container with image %s on platform %s\n", imageToUse, platform)
	}

	// Pull image if not exists (skip for custom local images)
	isCustomImage := strings.HasPrefix(imageToUse, ImageNamePrefix)
	_, err := c.client.ImageInspect(ctx, imageToUse)
	if err != nil && !isCustomImage {
		if c.debugMode {
			fmt.Printf("🔧 Debug: Pulling image %s for platform %s\n", imageToUse, platform)
		}

		pullOptions := image.PullOptions{
			Platform: platform,
		}
		reader, err := c.client.ImagePull(ctx, imageToUse, pullOptions)
		if err != nil {
			return err
		}
		defer reader.Close()
		io.Copy(io.Discard, reader)
	} else if err != nil && isCustomImage {
		return fmt.Errorf("custom image %s not found locally - it should be built with BuildImageFromDockerfile first", imageToUse)
	}

	// Parse platform string for ContainerCreate
	parts := strings.Split(platform, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid platform format %q: expected OS/arch", platform)
	}
	platformSpec := &specs.Platform{
		OS:           parts[0],
		Architecture: parts[1],
	}

	resp, err := c.client.ContainerCreate(ctx, config, hostConfig, nil, platformSpec, "")
	if err != nil {
		return err
	}

	c.containerID = resp.ID

	if c.debugMode {
		shortID := c.containerID[:12]
		fmt.Printf("🔧 Debug: Container created with ID %s\n", shortID)

		// Register container
		if c.registry != nil {
			if err := c.registry.Add(c.containerID, "kiro-eval", "debug", platform, imageToUse); err != nil {
				fmt.Printf("⚠️ Warning: Failed to register container in debug registry: %v\n", err)
			}
		}
	}

	// Update the container's image name to match what was actually used
	c.imageName = imageToUse

	return nil
}

// Start starts the container
func (c *Container) Start(ctx context.Context) error {
	if c.debugMode {
		fmt.Printf("🔧 Debug: Starting container %s\n", c.containerID[:12])
	}

	err := c.client.ContainerStart(ctx, c.containerID, container.StartOptions{})
	if err != nil {
		return err
	}

	if c.debugMode && c.registry != nil {
		c.registry.UpdateStatus(c.containerID, "running")
	}

	return nil
}

// CopyTo copies a file to the container
func (c *Container) CopyTo(ctx context.Context, destPath string, srcPath string) error {
	content, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}

	// Docker CopyToContainer requires a tar archive
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	header := &tar.Header{
		Name: filepath.Base(destPath),
		Mode: 0755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	if _, err := tw.Write(content); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}

	return c.client.CopyToContainer(ctx, c.containerID, filepath.Dir(destPath), &buf, container.CopyToContainerOptions{})
}

// CopyTreeTo recursively copies a host directory tree into the container under
// destParent/prefix. destParent must already exist in the container and be
// writable by the container user (e.g. the tmpfs /workspace). The tree is
// streamed as a single tar whose entries are rooted at prefix, so a call with
// destParent="/workspace", prefix=".kiro" reproduces the host tree at
// /workspace/.kiro/... Directory entries are written explicitly so the whole
// structure is created. Symlinked source entries are resolved to real content.
func (c *Container) CopyTreeTo(ctx context.Context, hostSrcDir, destParent, prefix string) error {
	if c.containerID == "" {
		return fmt.Errorf("container not created - call Create() before CopyTreeTo")
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Emit the prefix directory itself first.
	if prefix != "" {
		if err := tw.WriteHeader(&tar.Header{
			Name:     filepath.ToSlash(prefix) + "/",
			Mode:     0777,
			Typeflag: tar.TypeDir,
		}); err != nil {
			return err
		}
	}

	walkErr := filepath.Walk(hostSrcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(hostSrcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		// Resolve symlinks to real content (a linked .kiro entry lands as real
		// files in the container).
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, statErr := os.Stat(path)
			if statErr != nil {
				return statErr
			}
			if resolved.IsDir() {
				return c.tarTree(tw, path, filepath.Join(prefix, rel))
			}
			info = resolved
		}

		name := filepath.ToSlash(filepath.Join(prefix, rel))
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		if info.IsDir() {
			hdr.Name += "/"
			hdr.Mode = 0777
			return tw.WriteHeader(hdr)
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if walkErr != nil {
		return walkErr
	}
	if err := tw.Close(); err != nil {
		return err
	}

	return c.client.CopyToContainer(ctx, c.containerID, destParent, &buf, container.CopyToContainerOptions{})
}

// tarTree writes the contents of realDir into the tar rooted at relBase,
// resolving nested entries to real content.
func (c *Container) tarTree(tw *tar.Writer, realDir, relBase string) error {
	return filepath.Walk(realDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(realDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := filepath.ToSlash(filepath.Join(relBase, rel))
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		if info.IsDir() {
			hdr.Name += "/"
			hdr.Mode = 0777
			return tw.WriteHeader(hdr)
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

// CopyFromContainer copies the container path srcPath out to the host directory
// hostDestDir, extracting the tar Docker returns. It is the copy-out half of
// artifact collection: after the agent turn, produced files are streamed out of
// the container onto a host path that collectArtifacts can read. A missing
// srcPath is not an error — the agent may not have produced that artifact — in
// which case nothing is written.
func (c *Container) CopyFromContainer(ctx context.Context, srcPath, hostDestDir string) error {
	if c.containerID == "" {
		return fmt.Errorf("container not created - call Create() before CopyFromContainer")
	}

	reader, _, err := c.client.CopyFromContainer(ctx, c.containerID, srcPath)
	if err != nil {
		// Path not present in container (agent produced nothing there): treat as
		// no artifact rather than a hard failure.
		if strings.Contains(err.Error(), "No such container:path") ||
			strings.Contains(err.Error(), "no such file or directory") ||
			strings.Contains(err.Error(), "Could not find the file") {
			return nil
		}
		return err
	}
	defer reader.Close()

	if err := os.MkdirAll(hostDestDir, 0755); err != nil {
		return err
	}

	tr := tar.NewReader(reader)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Guard against path traversal from crafted tar entries.
		cleanName := filepath.Clean(hdr.Name)
		if strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) || strings.Contains(cleanName, string(os.PathSeparator)+".."+string(os.PathSeparator)) {
			continue
		}
		target := filepath.Join(hostDestDir, cleanName)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)|0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(hdr.Mode)|0600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		default:
			// Skip symlinks/other types coming out of the container.
		}
	}
	return nil
}

// Exec executes a command in the container
func (c *Container) Exec(ctx context.Context, cmd []string) error {
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	resp, err := c.client.ContainerExecCreate(ctx, c.containerID, execConfig)
	if err != nil {
		return err
	}

	return c.client.ContainerExecStart(ctx, resp.ID, container.ExecStartOptions{})
}

// ExecWithOutput executes a command and returns output
func (c *Container) ExecWithOutput(ctx context.Context, cmd []string) (string, error) {
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	resp, err := c.client.ContainerExecCreate(ctx, c.containerID, execConfig)
	if err != nil {
		return "", err
	}

	hijacked, err := c.client.ContainerExecAttach(ctx, resp.ID, container.ExecStartOptions{})
	if err != nil {
		return "", err
	}
	defer hijacked.Close()

	// Use stdcopy to properly demultiplex Docker streams
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	_, err = stdcopy.StdCopy(stdout, stderr, hijacked.Reader)
	if err != nil {
		return "", err
	}

	// Check execution result for exit code
	inspect, err := c.client.ContainerExecInspect(ctx, resp.ID)
	if err != nil {
		return "", err
	}

	output := strings.TrimSpace(stdout.String())
	if inspect.ExitCode != 0 {
		errOutput := strings.TrimSpace(stderr.String())
		return output, fmt.Errorf("command failed with exit code %d: %s", inspect.ExitCode, errOutput)
	}

	return output, nil
}

// Cleanup stops and removes the container
func (c *Container) Cleanup(ctx context.Context) error {
	return c.CleanupWithDebugInfo(ctx, false)
}

// CleanupWithDebugInfo stops and removes the container with debug awareness
func (c *Container) CleanupWithDebugInfo(ctx context.Context, failed bool) error {
	if c.containerID == "" {
		return nil
	}

	// In debug mode, preserve failed containers
	if c.debugMode && failed {
		if c.debugMode {
			shortID := c.containerID[:12]
			fmt.Printf("🔧 Debug: Preserving failed container %s for inspection\n", shortID)
			fmt.Printf("🔧 Debug commands:\n")
			fmt.Printf("  docker exec -it %s /bin/bash\n", shortID)
			fmt.Printf("  docker logs %s\n", shortID)
			fmt.Printf("  docker inspect %s\n", shortID)
		}

		if c.registry != nil {
			c.registry.UpdateStatus(c.containerID, "failed-preserved")
		}
		return nil
	}

	if c.debugMode {
		fmt.Printf("🔧 Debug: Stopping and removing container %s\n", c.containerID[:12])
	}

	timeout := 10 * time.Second
	timeoutSec := int(timeout.Seconds())
	err := c.client.ContainerStop(ctx, c.containerID, container.StopOptions{Timeout: &timeoutSec})
	if err != nil {
		return err
	}

	err = c.client.ContainerRemove(ctx, c.containerID, container.RemoveOptions{Force: true})

	// Remove from registry after successful cleanup
	if err == nil && c.registry != nil {
		c.registry.Remove(c.containerID)
	}

	return err
}

// GenerateDockerfileWithPlatform creates a custom Dockerfile with platform-specific kiro-cli installation
func (c *Container) GenerateDockerfileWithPlatform(projectPath, platform string) (string, error) {
	projects := DetectProject(projectPath)

	var dockerfile strings.Builder

	// Start with base image
	dockerfile.WriteString("FROM alpine:3.19\n\n")

	// Install essential tools
	dockerfile.WriteString("RUN apk add --no-cache \\\n")
	dockerfile.WriteString("    git \\\n")
	dockerfile.WriteString("    curl \\\n")
	dockerfile.WriteString("    bash \\\n")
	dockerfile.WriteString("    unzip \\\n")
	dockerfile.WriteString("    ca-certificates\n\n")

	// Add toolchain installations
	for _, project := range projects {
		template, err := c.loadTemplate(project.Type)
		if err != nil {
			return "", fmt.Errorf("loading template for %s: %w", project.Type, err)
		}
		dockerfile.WriteString(template)
		dockerfile.WriteString("\n")
	}

	// Add platform-specific kiro-cli installation
	kiroCLIInstall, err := addKiroCLIToDockerfile(platform)
	if err != nil {
		return "", fmt.Errorf("generating kiro-cli installation: %w", err)
	}
	dockerfile.WriteString(kiroCLIInstall)

	// Add user and workspace setup
	dockerfile.WriteString("RUN adduser -D -s /bin/bash sandbox\n")
	dockerfile.WriteString("RUN mkdir -p /workspace && chown sandbox:sandbox /workspace\n")
	dockerfile.WriteString("WORKDIR /workspace\n")
	dockerfile.WriteString("USER sandbox\n")
	dockerfile.WriteString("CMD [\"/bin/bash\"]\n")

	dockerfileContent := dockerfile.String()

	// Save dockerfile in debug mode
	if c.debugMode {
		id := c.containerID
		if id == "" {
			id = c.imageName
		}
		if err := debug.SaveDockerfile(dockerfileContent, id); err != nil {
			fmt.Printf("⚠️ Warning: Failed to save dockerfile: %v\n", err)
		}
	}

	return dockerfileContent, nil
}

// BuildImageFromDockerfile builds a Docker image from generated Dockerfile content
func (c *Container) BuildImageFromDockerfile(ctx context.Context, dockerfile string, imageName string, platform string) error {
	// Use image manager for reuse if available
	if c.imageManager != nil {
		reusedImage, err := c.imageManager.BuildForEvaluation(ctx, dockerfile, platform)
		if err != nil {
			return fmt.Errorf("image manager build failed: %w", err)
		}
		imageName = reusedImage
		c.imageName = imageName
		return nil
	}

	return c.buildImageDirect(ctx, dockerfile, imageName, platform)
}

// buildImageDirect performs the actual Docker image build without imageManager delegation.
func (c *Container) buildImageDirect(ctx context.Context, dockerfile string, imageName string, platform string) error {

	// Create tar archive with Dockerfile
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	dockerfileInfo := &tar.Header{
		Name: "Dockerfile",
		Size: int64(len(dockerfile)),
		Mode: 0644,
	}

	if err := tw.WriteHeader(dockerfileInfo); err != nil {
		return fmt.Errorf("writing dockerfile header: %w", err)
	}

	if _, err := tw.Write([]byte(dockerfile)); err != nil {
		return fmt.Errorf("writing dockerfile content: %w", err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar writer: %w", err)
	}

	// Build the image
	buildOptions := build.ImageBuildOptions{
		Tags:     []string{imageName},
		Platform: platform,
	}

	if c.debugMode {
		fmt.Printf("🔧 Debug: Building image %s for platform %s\n", imageName, platform)
	}

	resp, err := c.client.ImageBuild(ctx, bytes.NewReader(buf.Bytes()), buildOptions)
	if err != nil {
		return fmt.Errorf("building image: %w", err)
	}
	defer resp.Body.Close()

	// Use build output parser for clean logs
	parser := &DockerBuildOutputParser{Debug: c.debugMode}
	buildOutput, err := parser.ParseBuildStream(resp.Body)
	if err != nil {
		return fmt.Errorf("parsing build output: %w", err)
	}

	if c.debugMode {
		fmt.Printf("🔧 Debug: Build output:\n%s\n", buildOutput)
	}

	// Verify the image exists
	_, err = c.client.ImageInspect(ctx, imageName)
	if err != nil {
		return fmt.Errorf("image build completed but image %s not found: %w", imageName, err)
	}

	return nil
}

// GetCustomImageName generates a unique image name for the eval session
func (c *Container) GetCustomImageName(platform string) string {
	// Replace slashes in platform for image name compatibility
	safePlatform := strings.ReplaceAll(platform, "/", "-")
	timestamp := time.Now().UnixNano()

	// Add debug identifier for easy cleanup when in debug mode
	if c.debugMode {
		return fmt.Sprintf("kiro-eval-debug:%s-%d", safePlatform, timestamp)
	}
	return fmt.Sprintf("kiro-eval:%s-%d", safePlatform, timestamp)
}

// RemoveImage removes a Docker image by name
func (c *Container) RemoveImage(ctx context.Context, imageName string) ([]image.DeleteResponse, error) {
	return c.client.ImageRemove(ctx, imageName, image.RemoveOptions{Force: false, PruneChildren: true})
}

func (c *Container) loadTemplate(projectType ProjectType) (string, error) {
	templatePath := fmt.Sprintf("internal/eval/dockerfile/templates/%s.Dockerfile", string(projectType))
	content, err := os.ReadFile(templatePath)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// getKiroCLIDownloadURL returns the direct download URL for kiro-cli binary
func getKiroCLIDownloadURL(platform string) (string, error) {
	baseURL := "https://desktop-release.q.us-east-1.amazonaws.com/latest/"
	switch platform {
	case "linux/amd64":
		return baseURL + "kirocli-x86_64-linux-musl.zip", nil
	case "linux/arm64":
		return baseURL + "kirocli-aarch64-linux-musl.zip", nil
	default:
		return "", fmt.Errorf("unsupported platform: %s", platform)
	}
}

// addKiroCLIToDockerfile generates kiro-cli installation commands for Dockerfile
func addKiroCLIToDockerfile(platform string) (string, error) {
	downloadURL, err := getKiroCLIDownloadURL(platform)
	if err != nil {
		return "", err
	}

	var dockerfile strings.Builder
	dockerfile.WriteString("# Install kiro-cli\n")
	dockerfile.WriteString("RUN cd /tmp && \\\n")
	dockerfile.WriteString(fmt.Sprintf("    curl -fsSL %s -o kirocli.zip && \\\n", downloadURL))
	dockerfile.WriteString("    unzip -q kirocli.zip && \\\n")
	dockerfile.WriteString("    chmod 755 kirocli/bin/kiro-cli && \\\n")
	dockerfile.WriteString("    mv kirocli/bin/kiro-cli /usr/local/bin/kiro-cli && \\\n")
	dockerfile.WriteString("    rm -rf kirocli.zip kirocli\n\n")

	return dockerfile.String(), nil
}

// ValidateKiroCLI verifies kiro-cli installation at runtime
func (c *Container) ValidateKiroCLI(ctx context.Context, platform string) error {
	// Runtime verification only - installation happens during build
	if err := c.verifyKiroCLIInstallation(ctx); err != nil {
		return fmt.Errorf("kiro-cli verification failed: %w", err)
	}
	return nil
}

// verifyKiroCLIInstallation checks if kiro-cli is properly installed and functional
func (c *Container) verifyKiroCLIInstallation(ctx context.Context) error {
	// Check if binary exists and is executable
	_, err := c.ExecWithOutput(ctx, []string{"test", "-x", "/usr/local/bin/kiro-cli"})
	if err != nil {
		// Enhanced error reporting with file details
		if _, statErr := c.ExecWithOutput(ctx, []string{"test", "-f", "/usr/local/bin/kiro-cli"}); statErr == nil {
			// File exists but not executable - check permissions
			if perms, permErr := c.ExecWithOutput(ctx, []string{"ls", "-la", "/usr/local/bin/kiro-cli"}); permErr == nil {
				return fmt.Errorf("kiro-cli binary exists but is not executable: %s", perms)
			}
			return fmt.Errorf("kiro-cli binary exists but is not executable at /usr/local/bin/kiro-cli")
		}

		// Check if file exists in different location or if download failed
		if locations, locErr := c.ExecWithOutput(ctx, []string{"sh", "-c", "find / -name kiro-cli 2>/dev/null"}); locErr == nil && locations != "" {
			return fmt.Errorf("kiro-cli binary found in unexpected location: %s (expected /usr/local/bin/kiro-cli)", locations)
		}

		return fmt.Errorf("kiro-cli binary not found at /usr/local/bin/kiro-cli - installation may have failed during container build")
	}

	// Test version command with timeout and detailed errors
	version, err := c.ExecWithOutput(ctx, []string{"kiro-cli", "--version"})
	if err != nil {
		// Check file size to detect corruption
		if size, sizeErr := c.ExecWithOutput(ctx, []string{"stat", "-c", "%s", "/usr/local/bin/kiro-cli"}); sizeErr == nil {
			return fmt.Errorf("kiro-cli --version command failed (binary size: %s bytes, may be corrupted): %w", size, err)
		}
		return fmt.Errorf("kiro-cli --version command failed (binary may be corrupted): %w", err)
	}

	if version == "" {
		return fmt.Errorf("kiro-cli --version returned empty output - binary may be corrupted or incompatible")
	}

	if c.debugMode {
		size, _ := c.ExecWithOutput(ctx, []string{"stat", "-c", "%s", "/usr/local/bin/kiro-cli"})
		fmt.Printf("✅ kiro-cli installation verified: %s (binary size: %s bytes, platform: %s)\n", version, size, c.platform)
	} else {
		fmt.Printf("✅ kiro-cli installation verified: %s\n", version)
	}
	return nil
}

// Close closes the Docker client
func (c *Container) Close() error {
	return c.client.Close()
}
