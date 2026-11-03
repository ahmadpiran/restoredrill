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

func TestRedactDSNNeverLeaksAPassword(t *testing.T) {
	for _, dsn := range []string{
		"postgres://u:SECRET@h/db",
		"postgres://u@h/db?password=SECRET",
		"postgres://u@h/db?sslmode=require&password=SECRET&application_name=x",
		"postgres://u@h/db?sslpassword=SECRET",
		"postgres://u@h/db?PASSWORD=SECRET",
		"postgres://h/db?user=u&password=SECRET",
		"host=h user=u password=SECRET dbname=d",
		"host=h user=u password = SECRET",
		"host=h user=u password= SECRET",
		"host=h user=u PASSWORD=SECRET",
		"host=h user=u password='SECRET'",
		"host=h user=u password='two SECRET words'",
		`host=h user=u password='it\'s SECRET'`,
		`host=h user=u password=SE\ CRET`,
		"host=h user=u sslpassword=SECRET",
		"host=h user=u password='SECRET",
		"host=h user=u password",
		"postgres://u:SE#CRET@h/db",
		"postgres://u:12#SECRET@h/db",
		"postgres://u:12?SECRET@h/db",
		"postgres://u:12/SECRET@h/db",
	} {
		got := redactDSN(dsn)
		for _, leak := range []string{"SECRET", "CRET", "two", "words", `it\'s`} {
			if strings.Contains(got, leak) {
				t.Errorf("redactDSN(%q) = %q leaks %q", dsn, got, leak)
			}
		}
	}
}

func TestRedactDSNKeepsTheRest(t *testing.T) {
	cases := map[string]string{
		"postgres://u:pw@h:5432/db?sslmode=require&password=x&application_name=drill": "postgres://u@h:5432/db?sslmode=require&application_name=drill",
		"host=h port=5432 user=u password = x dbname='my db' sslmode=require":         "host=h port=5432 user=u dbname='my db' sslmode=require",
		"host=h user=u":                        "host=h user=u",
		"postgres://u@h/db":                    "postgres://u@h/db",
		"host=h user=u password='unterminated": "[redacted: unparseable]",
		"host=h user=u dangling":               "[redacted: unparseable]",
	}
	for in, want := range cases {
		if got := redactDSN(in); got != want {
			t.Errorf("redactDSN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitDSNPassword(t *testing.T) {
	cases := []struct{ in, wantDSN, wantPW string }{
		{"postgres://u:s3cr3t@h:5432/db?sslmode=require", "postgres://u@h:5432/db?sslmode=require", "s3cr3t"},
		{"postgres://u:p%40ss%2Fword@h/db", "postgres://u@h/db", "p@ss/word"},
		{"postgres://u@h/db?password=s3cr3t&sslmode=require", "postgres://u@h/db?sslmode=require", "s3cr3t"},
		{"host=h user=u password=s3cr3t dbname=d", "host=h user=u dbname=d", "s3cr3t"},
		{"host=h user=u password = s3cr3t", "host=h user=u", "s3cr3t"},
		{"host=h user=u password='two words'", "host=h user=u", "two words"},
		{`host=h user=u password='it\'s here'`, "host=h user=u", "it's here"},
		{`host=h user=u password=a\ b`, "host=h user=u", "a b"},
		{"host=h user=u sslpassword=keep password=s3cr3t", "host=h user=u sslpassword=keep", "s3cr3t"},
		{"postgres://u@h/db", "postgres://u@h/db", ""},
		{"host=h user=u", "host=h user=u", ""},
	}
	for _, c := range cases {
		gotDSN, gotPW := splitDSNPassword(c.in)
		if gotDSN != c.wantDSN || gotPW != c.wantPW {
			t.Errorf("splitDSNPassword(%q) = (%q, %q), want (%q, %q)", c.in, gotDSN, gotPW, c.wantDSN, c.wantPW)
		}
	}
}

func TestSplitDSNPasswordLeavesUnparseableDSNsAsTheyWere(t *testing.T) {
	for _, in := range []string{"postgres://u:12#34@h/db", "host=h password='unterminated", "host=h dangling"} {
		if gotDSN, gotPW := splitDSNPassword(in); gotDSN != in || gotPW != "" {
			t.Errorf("splitDSNPassword(%q) = (%q, %q), want it unchanged", in, gotDSN, gotPW)
		}
	}
}

func TestExistingConnectionPsqlGetsThePasswordFromTheEnvironment(t *testing.T) {
	sb := newExistingConnectionSandbox("postgres://u:s3cr3t@h/db?sslmode=disable")
	if strings.Contains(sb.dsn, "s3cr3t") {
		t.Fatalf("password stays in the psql argument: %q", sb.dsn)
	}
	found := false
	for _, e := range sb.hostPsqlEnv() {
		found = found || e == "PGPASSWORD=s3cr3t"
	}
	if !found {
		t.Error("PGPASSWORD not set for psql")
	}
	if env := newExistingConnectionSandbox("postgres://u@h/db").hostPsqlEnv(); strings.Contains(strings.Join(env, "\n")+"\n", "\nPGPASSWORD=s3cr3t") {
		t.Error("password leaked between sandboxes")
	}
}
