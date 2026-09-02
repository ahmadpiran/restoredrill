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
	mounts       []mount
	created      bool // true once "docker run" has actually created the container
	eng          engine
	database     string
	// dsn set: existing_connection. query/queryAs run psql on the host
	// against it instead of docker exec; exec is never called.
	dsn string
}

type mount struct {
	hostPath      string
	containerPath string
}

func newSandbox(eng engine, image string, readyTimeout time.Duration, mounts []mount) *sandbox {
	return &sandbox{
		name:         fmt.Sprintf("restoredrill-%d", time.Now().UnixNano()),
		image:        image,
		readyTimeout: readyTimeout,
		mounts:       mounts,
		eng:          eng,
	}
}

// created stays false: there is no container to clean up.
func newExistingConnectionSandbox(dsn string) *sandbox {
	return &sandbox{dsn: dsn, eng: postgresEngine}
}

func (s *sandbox) mountArgs() []string {
	var args []string
	for _, m := range s.mounts {
		args = append(args, "-v", m.hostPath+":"+m.containerPath)
	}
	return args
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

func (s *sandbox) startUninitialized() error {
	args := append([]string{"run", "-d", "--name", s.name, "--entrypoint", "bash"}, s.mountArgs()...)
	args = append(args, s.image, "-c", "sleep infinity")
	out, err := run(append([]string{"docker"}, args...)...)
	if err != nil {
		return fmt.Errorf("starting sandbox container: %v: %s", err, out)
	}
	s.created = true
	return nil
}

// Postgres refuses to start as root.
func (s *sandbox) startPostgres() error {
	return s.execDetachedAsUser("postgres", "postgres")
}

// pg_isready can go green mid-WAL-replay; only pg_is_in_recovery reaching
// false means the restore is done.
func (s *sandbox) waitForRecoveryComplete() error {
	deadline := time.Now().Add(s.readyTimeout)
	for time.Now().Before(deadline) {
		if out, err := s.query("SELECT pg_is_in_recovery()"); err == nil && out == "f" {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("postgres in container %s still in recovery after %s", s.name, s.readyTimeout)
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

func (s *sandbox) execEnv(env []string, args ...string) (string, error) {
	cmd := []string{"docker", "exec"}
	for _, e := range env {
		cmd = append(cmd, "-e", e)
	}
	return run(append(append(cmd, s.name), args...)...)
}

func (s *sandbox) execAsUser(user string, args ...string) (string, error) {
	return run(append([]string{"docker", "exec", "-u", user, s.name}, args...)...)
}

func (s *sandbox) execDetachedAsUser(user string, args ...string) error {
	out, err := run(append([]string{"docker", "exec", "-d", "-u", user, s.name}, args...)...)
	if err != nil {
		return fmt.Errorf("%v: %s", err, firstLine(out))
	}
	return nil
}

// query runs SQL against the restored database and returns the trimmed result.
func (s *sandbox) query(sql string) (string, error) {
	return s.queryAs("", sql)
}

// queryAs runs sql as role instead of the sandbox superuser, if the engine
// supports it.
func (s *sandbox) queryAs(role, sql string) (string, error) {
	out, err := s.eng.query(s, role, sql)
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

func runEnv(env []string, args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}
