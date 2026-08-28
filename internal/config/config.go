package config

import (
	"fmt"
	"os"
	pathpkg "path"
	"strings"
	"time"

	"github.com/ahmadpiran/restoredrill/internal/backupformat"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Backup   Backup   `yaml:"backup"`
	Postgres Postgres `yaml:"postgres"`
	Sandbox  Sandbox  `yaml:"sandbox"`
	Checks   Checks   `yaml:"checks"`
	Notify   Notify   `yaml:"notify"`
	Output   Output   `yaml:"output"`
	Report   Report   `yaml:"report"`
}

type Backup struct {
	Source          string `yaml:"source"`
	Format          string `yaml:"format"`
	S3ObjectPattern string `yaml:"s3_object_pattern"`
	// GlobalsSource is always an exact file: no S3 prefix selection.
	GlobalsSource string `yaml:"globals_source"`
}

type Postgres struct {
	Image string `yaml:"image"`
}

type Sandbox struct {
	// Keep controls whether the restored container survives the drill for
	// human inspection: never (default), on-failure, or always.
	Keep string `yaml:"keep"`
}

// defaultMinSizeBytes is the floor when min_size_bytes is unset: tiny, just
// enough to catch an empty file, not a stand-in for a real threshold.
const defaultMinSizeBytes = 100

type Checks struct {
	MinSizeBytes      int64         `yaml:"min_size_bytes"`
	ArchiveIntegrity  *bool         `yaml:"archive_integrity"`
	RPOTarget         string        `yaml:"rpo_target"`
	RPOTargetDuration time.Duration `yaml:"-"`

	MinTables         int           `yaml:"min_tables"`
	SequenceIntegrity bool          `yaml:"sequence_integrity"`
	RowCounts         []RowCount    `yaml:"row_counts"`
	Queries           []Assertion   `yaml:"queries"`
	RTOTarget         string        `yaml:"rto_target"`
	RTOTargetDuration time.Duration `yaml:"-"`

	VerifyAsRole string `yaml:"verify_as_role"`
}

type RowCount struct {
	Table string `yaml:"table"`
	Min   int64  `yaml:"min"`
}

type Assertion struct {
	Name string `yaml:"name"`
	SQL  string `yaml:"sql"`
}

type Notify struct {
	WebhookURL      string `yaml:"webhook_url"`
	SlackWebhookURL string `yaml:"slack_webhook_url"`
}

type Output struct {
	PrometheusTextfile string `yaml:"prometheus_textfile"`
}

type Report struct {
	Path string `yaml:"path"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if c.Backup.Source == "" {
		return nil, fmt.Errorf("%s: backup.source is required", path)
	}
	if c.Backup.Format == "" {
		c.Backup.Format = "pg_dump_custom"
	}
	if !backupformat.Valid(c.Backup.Format) {
		return nil, fmt.Errorf("%s: backup.format must be one of %v, got %q", path, backupformat.Names(), c.Backup.Format)
	}
	isS3Prefix := strings.HasPrefix(c.Backup.Source, "s3://") && strings.HasSuffix(c.Backup.Source, "/")
	if c.Backup.S3ObjectPattern == "" && isS3Prefix && !backupformat.Sniffable(c.Backup.Format) {
		return nil, fmt.Errorf("%s: backup.s3_object_pattern is required for backup.format %q (no content signature) with a prefix backup.source; set a pattern like \"*.sql\" to identify the backup among sibling objects", path, c.Backup.Format)
	}
	if c.Backup.S3ObjectPattern != "" {
		if _, err := pathpkg.Match(c.Backup.S3ObjectPattern, "x"); err != nil {
			return nil, fmt.Errorf("%s: backup.s3_object_pattern %q is not a valid pattern: %w", path, c.Backup.S3ObjectPattern, err)
		}
	}
	if c.Checks.VerifyAsRole != "" && c.Backup.GlobalsSource == "" {
		return nil, fmt.Errorf("%s: checks.verify_as_role requires backup.globals_source (the role must exist in the sandbox before checks can connect as it)", path)
	}
	if c.Postgres.Image == "" {
		c.Postgres.Image = "postgres:16"
	}
	switch c.Sandbox.Keep {
	case "":
		c.Sandbox.Keep = "never"
	case "never", "on-failure", "always":
	default:
		return nil, fmt.Errorf("%s: sandbox.keep must be never, on-failure, or always", path)
	}
	switch {
	case c.Checks.MinSizeBytes < 0:
		c.Checks.MinSizeBytes = 0 // explicit opt-out
	case c.Checks.MinSizeBytes == 0:
		c.Checks.MinSizeBytes = defaultMinSizeBytes
	}
	if d, err := parseOptionalDuration(path, "rpo_target", c.Checks.RPOTarget); err != nil {
		return nil, err
	} else {
		c.Checks.RPOTargetDuration = d
	}
	if d, err := parseOptionalDuration(path, "rto_target", c.Checks.RTOTarget); err != nil {
		return nil, err
	} else {
		c.Checks.RTOTargetDuration = d
	}
	if c.Report.Path == "" {
		c.Report.Path = "restoredrill-report.json"
	}
	return &c, nil
}

func parseOptionalDuration(path, field, s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%s: checks.%s: invalid duration %q: %w", path, field, s, err)
	}
	return d, nil
}
