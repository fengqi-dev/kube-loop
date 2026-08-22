package sshserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kballard/go-shellquote"
	"github.com/pkg/sftp"
)

type sftpHandler struct {
	executor Executor
	target   Target
}

func newSFTPHandler(executor Executor, target Target) *sftpHandler {
	return &sftpHandler{executor: executor, target: target}
}

func (h *sftpHandler) Fileread(request *sftp.Request) (io.ReaderAt, error) {
	file, err := h.downloadFile(request.Context(), cleanRemotePath(request.Filepath))
	if err != nil {
		return nil, err
	}
	return &downloadFile{File: file}, nil
}

func (h *sftpHandler) Filewrite(request *sftp.Request) (io.WriterAt, error) {
	remotePath := cleanRemotePath(request.Filepath)
	flags := request.Pflags()
	if flags.Excl {
		if _, err := h.stat(request.Context(), remotePath); err == nil {
			return nil, os.ErrExist
		}
	}
	var file *os.File
	var err error
	if !flags.Trunc {
		file, err = h.downloadFile(request.Context(), remotePath)
	} else {
		file, err = os.CreateTemp("", "kubeloop-sftp-upload-*")
	}
	if err != nil {
		// OpenSSH scp opens uploads with WRITE|CREAT but without TRUNC.
		// A missing Pod file is reported by Kubernetes exec as a generic
		// command error, so it cannot reliably satisfy os.ErrNotExist.
		if flags.Creat {
			file, err = os.CreateTemp("", "kubeloop-sftp-upload-*")
		}
		if err != nil {
			return nil, err
		}
	}
	if flags.Trunc {
		if err := file.Truncate(0); err != nil {
			_ = file.Close()
			_ = os.Remove(file.Name())
			return nil, err
		}
	}
	mode := os.FileMode(0o644)
	if attrs := request.Attributes(); attrs != nil && request.AttrFlags().Permissions {
		mode = attrs.FileMode().Perm()
	}
	return &uploadFile{
		File: file,
		closeRemote: func() error {
			return h.upload(request.Context(), remotePath, file, mode)
		},
	}, nil
}

func (h *sftpHandler) Filelist(request *sftp.Request) (sftp.ListerAt, error) {
	remotePath := cleanRemotePath(request.Filepath)
	switch request.Method {
	case "List":
		items, err := h.list(request.Context(), remotePath)
		if err != nil {
			return nil, err
		}
		return fileInfoList(items), nil
	case "Stat", "Lstat", "Readlink":
		item, err := h.stat(request.Context(), remotePath)
		if err != nil {
			return nil, err
		}
		return fileInfoList([]os.FileInfo{item}), nil
	default:
		return nil, fmt.Errorf("unsupported SFTP list operation %q", request.Method)
	}
}

func (h *sftpHandler) Lstat(request *sftp.Request) (sftp.ListerAt, error) {
	item, err := h.stat(request.Context(), cleanRemotePath(request.Filepath))
	if err != nil {
		return nil, err
	}
	return fileInfoList([]os.FileInfo{item}), nil
}

func (h *sftpHandler) RealPath(raw string) (string, error) {
	return cleanRemotePath(raw), nil
}

func (h *sftpHandler) Readlink(raw string) (string, error) {
	var stdout bytes.Buffer
	err := h.exec(context.Background(), "readlink "+shellquote.Join(cleanRemotePath(raw)), &stdout)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (h *sftpHandler) Filecmd(request *sftp.Request) error {
	remotePath := cleanRemotePath(request.Filepath)
	var command string
	switch request.Method {
	case "Mkdir":
		command = "mkdir -- " + shellquote.Join(remotePath)
	case "Rmdir":
		command = "rmdir -- " + shellquote.Join(remotePath)
	case "Remove":
		command = "rm -f -- " + shellquote.Join(remotePath)
	case "Rename", "PosixRename":
		command = "mv -- " + shellquote.Join(remotePath) + " " + shellquote.Join(cleanRemotePath(request.Target))
	case "Symlink":
		command = "ln -s -- " + shellquote.Join(
			request.Filepath,
		) + " " + shellquote.Join(
			cleanRemotePath(request.Target),
		)
	case "Link":
		command = "ln -- " + shellquote.Join(remotePath) + " " + shellquote.Join(cleanRemotePath(request.Target))
	case "Setstat":
		return h.setstat(request.Context(), remotePath, request)
	default:
		return fmt.Errorf("unsupported SFTP command %q", request.Method)
	}
	return h.exec(request.Context(), command, io.Discard)
}

func (h *sftpHandler) PosixRename(request *sftp.Request) error {
	return h.Filecmd(request)
}
