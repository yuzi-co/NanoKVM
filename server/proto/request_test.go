package proto

import (
	"sync"
	"testing"
)

// The validator is now built once and shared, so these guard both that
// validation still works and that sharing it is safe.

func TestValidateRequestAcceptsCompleteRequest(t *testing.T) {
	if err := ValidateRequest(&LoginReq{Username: "admin", Password: "secret"}); err != nil {
		t.Fatalf("a complete request should validate: %s", err)
	}
}

func TestValidateRequestRejectsMissingRequiredField(t *testing.T) {
	if err := ValidateRequest(&LoginReq{Username: "admin"}); err == nil {
		t.Fatal("a request missing a required field must be rejected")
	}
}

func TestValidateRequestIsSafeForConcurrentUse(t *testing.T) {
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			if i%2 == 0 {
				if err := ValidateRequest(&LoginReq{Username: "admin", Password: "secret"}); err != nil {
					t.Errorf("valid request rejected: %s", err)
				}
				return
			}

			if err := ValidateRequest(&RunScriptReq{Name: "a.sh"}); err == nil {
				t.Error("invalid request accepted")
			}
		}(i)
	}

	wg.Wait()
}
