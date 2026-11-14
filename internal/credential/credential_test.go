package credential

import (
	"context"
	"errors"
	"testing"
)

type fakeReader struct {
	value string
	err   error
}

func (f *fakeReader) ReadField(_ context.Context, path, field string) (string, error) {
	if path != "kv/data/git-proxy/github" || field != "token" {
		return "", errors.New("unexpected path or field")
	}

	return f.value, f.err
}

func TestLoadNeedsAReadableCredential(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{err: errors.New("permission denied")}
	if _, err := Load(t.Context(), reader, "kv/data/git-proxy/github", "token"); err == nil {
		t.Fatal("Load succeeded without a credential")
	}
}

func TestRefreshPicksUpRotationAndKeepsTheLastGoodValue(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{value: "ghp_first"}
	credential, err := Load(t.Context(), reader, "kv/data/git-proxy/github", "token")
	if err != nil || credential.Token() != "ghp_first" {
		t.Fatalf("Load = %v, %v", credential, err)
	}

	reader.value = "ghp_rotated"
	if err := credential.Refresh(t.Context()); err != nil || credential.Token() != "ghp_rotated" {
		t.Fatalf("after rotation: %q, %v", credential.Token(), err)
	}

	reader.err = errors.New("OpenBao sealed")
	if err := credential.Refresh(t.Context()); err == nil || credential.Token() != "ghp_rotated" {
		t.Fatalf("after failed refresh: %q, %v; want the last good value and an error", credential.Token(), err)
	}
}
