# Issue #201: Enhance builder-conventions skill with mandatory template synchronization

**Closes #201**

## Problem Analysis

The howmux project has a unique self-hosting scenario where it uses howmux to build itself. This creates a critical synchronization requirement between live project files and embedded templates that are deployed to users via `howmux init` and `howmux update` commands.

### Root Cause
- Agent changes in PRs #198 and #200 modified `.kiro/agents/` files
- Corresponding template files in `cmd/howmux/templates/kiro/agents/` were not updated
- Users running `howmux init` or `howmux update` receive outdated templates
- Current builder-conventions skill documents sync requirements but lacks enforcement

### Impact
- New howmux installations get outdated agent configurations
- Template-synchronized files become inconsistent across the project
- Manual sync verification is error-prone and easily forgotten

## Solution Approach

Enhance the builder-conventions skill with mandatory template synchronization that includes:

1. **Mandatory Sync Commands**: Clear, executable copy commands for all template-synchronized directories
2. **Blocking Verification**: Script that prevents task completion until sync is confirmed
3. **Exclusion Patterns**: Filter out project-specific *-conventions skills from sync
4. **Error Exit Strategy**: Verification script exits with error if files don't match

## Relevant Files

### Files to Modify
- `.kiro/skills/builder-conventions/SKILL.md` - Main skill file to enhance

### Template Sync Mappings (One-way: Live → Template)

#### Agent Files
- **Live**: `.kiro/agents/*.json`, `.kiro/agents/*-prompt.md`
- **Template**: `cmd/howmux/templates/kiro/agents/*.json`, `cmd/howmux/templates/kiro/agents/*-prompt.md`
- **Exclusions**: None (all agent files sync)

#### Scripts
- **Live**: `.howmux/scripts/*.sh`
- **Template**: `cmd/howmux/templates/howmux/scripts/*.sh`
- **Exclusions**: None (all scripts sync)

#### Themes
- **Live**: `.howmux/themes/*.yaml`
- **Template**: `cmd/howmux/templates/howmux/themes/*.yaml`
- **Exclusions**: None (all themes sync)

#### Evaluation Files
- **Live**: `.howmux/evals/fixtures/*`, `.howmux/evals/rubrics/*`, `.howmux/evals/cases/**/*`
- **Template**: `cmd/howmux/templates/howmux/evals/fixtures/*`, etc.
- **Exclusions**: `.howmux/evals/results/` (not synced - runtime generated)

#### Skills (Project-Specific Exclusions)
- **Live**: `.kiro/skills/` (excluding *-conventions patterns)
- **Template**: Not applicable (*-conventions skills are project-specific)
- **Exclusions**: `*-conventions*` patterns (builder-conventions, planner-conventions, etc.)

## Team Orchestration

This is a single-agent task focused on enhancing one skill file. No parallel coordination required.

**Builder Agent Tasks**:
1. All tasks are executed sequentially by a single builder agent
2. Each task depends on the previous task's completion
3. Verification task blocks completion until sync is confirmed

## Step-by-Step Task Breakdown

### Task 1: Add Mandatory Sync Section
**Acceptance Criteria**:
- Add "Mandatory Template Synchronization" section to builder-conventions skill
- Include clear copy commands for each synchronized directory type
- Add exclusion patterns for *-conventions skills
- Provide examples of complete sync workflow
**Dependencies**: None

### Task 2: Implement Blocking Verification Script
**Acceptance Criteria**:
- Create verification commands that check file differences
- Script exits with error (non-zero) if any template-synchronized files don't match
- Verification must be run before task completion
- Include clear error messages indicating which files are out of sync
**Dependencies**: Task 1

### Task 3: Add Workflow Integration Requirements
**Acceptance Criteria**:
- Document that sync must happen before task completion
- Add sync verification to existing QA workflow requirements
- Include examples showing sync + verification workflow
- Make sync checking part of standard builder completion process
**Dependencies**: Task 1, Task 2

### Task 4: Test and Validate Implementation
**Acceptance Criteria**:
- Manually test sync commands work correctly
- Verify exclusion patterns filter out *-conventions skills
- Confirm verification script properly detects differences
- Ensure error exit codes work as expected
**Dependencies**: Task 1, Task 2, Task 3

## Validation Commands

```bash
# Test sync commands work
cp .kiro/agents/builder.json cmd/howmux/templates/kiro/agents/builder.json
echo $? # Should be 0

# Test verification detects differences
echo "test" >> .kiro/agents/builder.json
diff .kiro/agents/builder.json cmd/howmux/templates/kiro/agents/builder.json
echo $? # Should be 1 (differences detected)

# Test exclusion patterns
ls .kiro/skills/*-conventions*/SKILL.md | wc -l # Should show excluded skills
find cmd/howmux/templates/ -name "*-conventions*" | wc -l # Should be 0

# Verify builder-conventions skill exists and is enhanced
grep -q "Mandatory Template Synchronization" .kiro/skills/builder-conventions/SKILL.md
echo $? # Should be 0 (section found)

# Test complete sync verification workflow
bash -c 'cd $(pwd) && [verification script commands here]'
echo $? # Should be 0 when files are synced, 1 when out of sync
```

## Implementation Details

### Exclusion Pattern Logic
The *-conventions skills are project-specific and should never be distributed in templates:
- `builder-conventions` - Project-specific build patterns
- `planner-conventions` - Project-specific analysis methodology

### Sync Command Structure
```bash
# Agent files (all files sync)
cp .kiro/agents/*.json cmd/howmux/templates/kiro/agents/
cp .kiro/agents/*.md cmd/howmux/templates/kiro/agents/

# Scripts (all files sync) 
cp .howmux/scripts/*.sh cmd/howmux/templates/howmux/scripts/

# Themes (all files sync)
cp .howmux/themes/*.yaml cmd/howmux/templates/howmux/themes/

# Evals (excluding results directory)
cp .howmux/evals/fixtures/* cmd/howmux/templates/howmux/evals/fixtures/
cp .howmux/evals/rubrics/* cmd/howmux/templates/howmux/evals/rubrics/
cp -r .howmux/evals/cases/* cmd/howmux/templates/howmux/evals/cases/
```

### Verification Script Logic
```bash
#!/bin/bash
# Template sync verification
sync_error=0

# Check agent files
for file in .kiro/agents/*.json; do
  template_file="cmd/howmux/templates/kiro/agents/$(basename "$file")"
  if ! diff -q "$file" "$template_file" >/dev/null 2>&1; then
    echo "ERROR: $file differs from $template_file"
    sync_error=1
  fi
done

# [Additional checks for scripts, themes, evals...]

exit $sync_error
```

This design ensures complete synchronization between live and template files while preserving project-specific skills and providing robust verification mechanisms.
