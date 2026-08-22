package sshserver

import (
	"path"
	"strings"
)

func cleanRemotePath(raw string) string {
	cleaned := path.Clean("/" + strings.TrimSpace(raw))
	if cleaned == "." || cleaned == "" {
		return "/"
	}
	return cleaned
}

func splitRemotePath(remotePath string) (string, string) {
	remotePath = cleanRemotePath(remotePath)
	if remotePath == "/" {
		return "/", "."
	}
	return path.Dir(remotePath), path.Base(remotePath)
}

func archiveNameMatches(name, base string) bool {
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	base = strings.TrimPrefix(path.Clean("/"+base), "/")
	return name == base
}
