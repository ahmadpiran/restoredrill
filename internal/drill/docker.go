package drill

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// sandbox is a disposable Postgres container driven via the docker CLI, so the
// agent's only runtime dependency is docker itself.
type sandbox struct {
	name         string
	image        string
	readyTimeout time.Duration
	created      bool // true once "docker run" has actually created the container
}

func newSandbox(image string, readyTimeout time.Duration) *sandbox {
	return &sandbox{
		name:         fmt.Sprintf("restoredrill-%d", time.Now().UnixNano()),
		image:        image,
		readyTimeout: readyTimeout,
	}
}

func (s *sandbox) start() error {
	out, err := run("docker", "run", "-d", "--name", s.name,
		"-e", "POSTGRES_PASSWORD=restoredrill", s.image)
	if err != nil {
		return fmt.Errorf("starting postgres container: %v: %s", err, out)
	}
	// Exists from here on, even if readiness polling below times out.
	s.created = true

	deadline := time.Now().Add(s.readyTimeout)
	for time.Now().Before(deadline) {
		if _, err := s.exec("pg_isready", "-U", "postgres"); err == nil {
			// The official image restarts postgres once during first init;
			// require two consecutive successes so we don't catch the gap.
			time.Sleep(2 * time.Second)
			if _, err := s.exec("pg_isready", "-U", "postgres"); err == nil {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("postgres in container %s not ready after %s", s.name, s.readyTimeout)
}

// destroy removes the container. Callers should check the error, not
// discard it: a failed teardown is a leak worth recording.
func (s *sandbox) destroy() error {
	out, err := run("docker", "rm", "-f", s.name)
	if err != nil {
		return fmt.Errorf("%v: %s", err, firstLine(out))
	}
	return nil
}

func (s *sandbox) copyIn(local, remote string) error {
	out, err := run("docker", "cp", local, s.name+":"+remote)
	if err != nil {
		return fmt.Errorf("docker cp: %v: %s", err, out)
	}
	return nil
}

func (s *sandbox) exec(args ...string) (string, error) {
	return run(append([]string{"docker", "exec", s.name}, args...)...)
}

// query runs SQL against the restored database and returns the trimmed result.
func (s *sandbox) query(sql string) (string, error) {
	return s.queryAs("", sql)
}

// queryAs prefixes sql with "SET ROLE role" so it runs as role instead of
// postgres (a superuser can SET ROLE without a membership check). -q
// suppresses the "SET" status line psql would otherwise print ahead of the
// result, even under -t.
func (s *sandbox) queryAs(role, sql string) (string, error) {
	args := []string{"-U", "postgres", "-d", "postgres", "-t", "-A"}
	if role != "" {
		args = append(args, "-q")
		sql = fmt.Sprintf("SET ROLE %s; %s", quoteSingleIdent(role), sql)
	}
	args = append(args, "-c", sql)
	out, err := s.exec(append([]string{"psql"}, args...)...)
	return strings.TrimSpace(out), err
}

func run(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}
