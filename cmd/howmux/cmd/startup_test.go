package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/matthiashowellyopp/howmux/internal/config"
	"github.com/matthiashowellyopp/howmux/internal/review"
)

func TestValidateAuditorModel(t *testing.T) {
	cases := map[string]struct {
		checkErr error
		wantErr  bool
		wantWarn bool
	}{
		"valid model":      {checkErr: nil},
		"unknown model":    {checkErr: errors.New(`review.auto_post.auditor_model "calude-sonnet-5.5" is not a model kiro-cli offers`), wantErr: true},
		"list unavailable": {checkErr: fmt.Errorf("%w: kiro-cli not found", review.ErrModelListUnavailable), wantWarn: true},
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
			var warn bytes.Buffer
			err := validateAuditorModel(context.Background(), cfg, &warn)

			if gotModel != "some-model" {
				t.Errorf("checked model %q, want the configured one", gotModel)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "calude-sonnet-5.5") {
				t.Errorf("err %q should name the bad model", err)
			}
			if got := warn.Len() > 0; got != tc.wantWarn {
				t.Errorf("warning written = %v (%q), want %v", got, warn.String(), tc.wantWarn)
			}
		})
	}
}
