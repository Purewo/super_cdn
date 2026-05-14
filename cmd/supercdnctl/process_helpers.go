package main

import "os/exec"

func runCommand(name string, args, env []string) (string, int, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	raw, err := cmd.CombinedOutput()
	if err == nil {
		return string(raw), 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(raw), exitErr.ExitCode(), err
	}
	return string(raw), -1, err
}
