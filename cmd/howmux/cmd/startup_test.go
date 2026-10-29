package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/review"
)

func TestValidateAuditorModel(t *testing.T) {
	unknown := errors.New(`review.auto_post.auditor_model "calude-sonnet-5.5" is not a model kiro-cli offers`)
	unavailable := fmt.Errorf("%w: kiro-cli not found", review.ErrModelListUnavailable)

	cases := map[string]struct {
		checkErr   error
		wantErr    bool
		wantNotice bool
	}{
		"valid model":      {checkErr: nil},
		"unknown model":    {checkErr: unknown, wantErr: true},
		"list unavailable": {checkErr: unavailable, wantNotice: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			orig := checkAuditorModelFunc
			var gotModel string
			checkAuditorModelFunc = func(_ context.Context, model string) error {
				gotModel = model
				return tc.checkErr
			}
			t.Cleanup(func() { checkAuditorModelFunc = orig })

			cfg := &config.Config{}
			cfg.Review.AutoPost.AuditorModel = "some-model"
			notice, err := validateAuditorModel(context.Background(), cfg)

			if gotModel != "some-model" {
				t.Errorf("checked model %q, want the configured one", gotModel)
			}
			if tc.wantErr {
				if err == nil {
					t.Fatal("err = nil, want an error")
				}
				// What this function adds: the %w wrap and the prefix.
				if !errors.Is(err, tc.checkErr) {
					t.Errorf("err %v does not wrap the check error", err)
				}
				if !strings.HasPrefix(err.Error(), "invalid config: ") {
					t.Errorf("err %q lacks the invalid config: prefix", err)
				}
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if got := notice != ""; got != tc.wantNotice {
				t.Errorf("notice = %q, want notice %v", notice, tc.wantNotice)
			}
			if tc.wantNotice && !strings.Contains(notice, "kiro-cli not found") {
				t.Errorf("notice %q should carry the underlying cause", notice)
			}
		})
	}
}
