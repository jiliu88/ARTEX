//go:build !linux

package taskbrowser

import "os/exec"

func prepareProcess(cmd *exec.Cmd)            {}
func processTaskIDs(root string) []string     { return nil }
func terminateTask(root, id string, keep int) {}
