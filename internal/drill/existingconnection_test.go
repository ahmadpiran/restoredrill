package drill

import (
	"strings"
	"testing"
)

func TestRedactDSNStripsPasswordFromURIForm(t *testing.T) {
	got := redactDSN("postgres://app_user:s3cr3t@db.example.com:5432/app")
	if strings.Contains(got, "s3cr3t") {
		t.Fatalf("expected password stripped, got %q", got)
	}
	if !strings.Contains(got, "app_user") {
		t.Errorf("expected username kept, got %q", got)
	}
	if !strings.Contains(got, "db.example.com") {
		t.Errorf("expected host kept, got %q", got)
	}
}

func TestRedactDSNStripsPasswordFromKeywordForm(t *testing.T) {
	got := redactDSN("host=db.example.com port=5432 user=app_user password=s3cr3t dbname=app")
	if strings.Contains(got, "s3cr3t") {
		t.Fatalf("expected password stripped, got %q", got)
	}
	if !strings.Contains(got, "app_user") || !strings.Contains(got, "db.example.com") {
		t.Errorf("expected non-password fields kept, got %q", got)
	}
}

func TestRedactDSNNoPasswordUnaffected(t *testing.T) {
	got := redactDSN("postgres://app_user@db.example.com/app")
	if !strings.Contains(got, "app_user") || !strings.Contains(got, "db.example.com") {
		t.Errorf("expected DSN preserved, got %q", got)
	}
}
