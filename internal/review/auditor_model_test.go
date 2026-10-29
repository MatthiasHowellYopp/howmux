package review

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func fakeModelList(t *testing.T, ids []string, err error) *int {
	t.Helper()
	calls := 0
	orig := listModelsFunc
	listModelsFunc = func(context.Context) ([]string, error) {
		calls++
		return ids, err
	}
	t.Cleanup(func() { listModelsFunc = orig })
	return &calls
}

func TestCheckAuditorModel(t *testing.T) {
	models := []string{"auto", "claude-sonnet-5.5", "claude-haiku-4.5"}

	t.Run("empty model is valid and skips the lookup", func(t *testing.T) {
		calls := fakeModelList(t, models, nil)
		if err := CheckAuditorModel(context.Background(), "  "); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
		if *calls != 0 {
			t.Errorf("listModels called %d times, want 0", *calls)
		}
	})

	t.Run("known model is valid", func(t *testing.T) {
		fakeModelList(t, models, nil)
		if err := CheckAuditorModel(context.Background(), " claude-sonnet-5.5 "); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("typo is rejected and names the model and the choices", func(t *testing.T) {
		fakeModelList(t, models, nil)
		err := CheckAuditorModel(context.Background(), "calude-sonnet-5.5")
		if err == nil {
			t.Fatal("err = nil, want an unknown-model error")
		}
		if errors.Is(err, ErrModelListUnavailable) {
			t.Errorf("unknown model must not be reported as list-unavailable: %v", err)
		}
		for _, want := range []string{`"calude-sonnet-5.5"`, "claude-sonnet-5.5", "claude-haiku-4.5"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err %q does not mention %s", err, want)
			}
		}
	})

	t.Run("list failure wraps ErrModelListUnavailable", func(t *testing.T) {
		fakeModelList(t, nil, errors.New("kiro-cli not found"))
		err := CheckAuditorModel(context.Background(), "claude-sonnet-5.5")
		if !errors.Is(err, ErrModelListUnavailable) {
			t.Errorf("err = %v, want it to wrap ErrModelListUnavailable", err)
		}
	})
}
