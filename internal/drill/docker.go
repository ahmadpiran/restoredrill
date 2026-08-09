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
	name    string
	image   string
	created bool // true once "docker run" has actually created the container
}

func newSandbox(image string) *sandbox {
	return &sandbox{
		name:  fmt.Sprintf("restoredrill-%d", time.Now().UnixNano()),
		image: image,
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

	deadline := time.Now().Add(120 * time.Second)
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
	return fmt.Errorf("postgres in container %s not ready after 120s", s.name)
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
	out, err := s.exec("psql", "-U", "postgres", "-d", "postgres", "-tAc", sql)
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
