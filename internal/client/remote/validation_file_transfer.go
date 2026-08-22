package remote

import (
	"errors"
	"path"
	"strings"

	"github.com/google/uuid"
)

func validateFileTransferSpec(spec *FileTransferSpec) error {
	spec.Direction = strings.ToLower(strings.TrimSpace(spec.Direction))
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	spec.Pod = strings.TrimSpace(spec.Pod)
	spec.Container = strings.TrimSpace(spec.Container)
	spec.Checksum = strings.ToLower(strings.TrimSpace(spec.Checksum))
	spec.ResumeID = strings.ToLower(strings.TrimSpace(spec.ResumeID))
	if spec.Direction != remoteDirectionUpload && spec.Direction != remoteDirectionDownload {
		return errors.New("file transfer direction must be upload or download")
	}
	if spec.Kind != remoteKindFile && spec.Kind != remoteKindDirectory {
		return errors.New("file transfer kind must be file or directory")
	}
	if !validDNSSubdomain(spec.Pod) || (spec.Container != "" && !validDNSLabel(spec.Container)) {
		return errors.New("file transfer Pod or container is invalid")
	}
	if err := validateRemotePath(spec.RemotePath); err != nil {
		return err
	}
	switch {
	case spec.Direction == remoteDirectionUpload:
		if spec.Size == 0 || spec.Offset > spec.Size || !validDigest(spec.Checksum) {
			return errors.New("file upload size, offset or checksum is invalid")
		}
		if spec.Kind == remoteKindDirectory && spec.Offset != 0 {
			return errors.New("directory upload cannot resume from a byte offset")
		}
		if spec.ResumeID != "" {
			if spec.Kind != remoteKindFile {
				return errors.New("only file uploads support a Resume ID")
			}
			if _, err := uuid.Parse(spec.ResumeID); err != nil {
				return errors.New("file upload Resume ID is invalid")
			}
		}
	case spec.Size != 0 || spec.Checksum != "" || spec.Overwrite:
		return errors.New("file download metadata must be determined by the Gateway")
	case spec.Kind == remoteKindDirectory && spec.Offset != 0:
		return errors.New("directory download cannot resume from a byte offset")
	case spec.ResumeID != "":
		return errors.New("file downloads do not accept a Resume ID")
	}
	return nil
}

func validateRemotePath(value string) error {
	if value == "" || len(value) > 4096 || value[0] != '/' || value == "/" || strings.Contains(value, "\\") ||
		path.Clean(value) != value {
		return errors.New("file transfer remote path is invalid")
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return errors.New("file transfer remote path is invalid")
		}
	}
	return nil
}

func validateFileTransferTask(task FileTransferTask, session Session) (FileTransferTask, error) {
	if _, err := uuid.Parse(
		task.ID,
	); err != nil || task.SessionID != session.ID || task.Namespace != session.Namespace ||
		!task.State.Valid() ||
		task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() ||
		task.ExpiresAt.IsZero() {
		return FileTransferTask{}, errors.New("gateway returned an incomplete file transfer Task")
	}
	spec := FileTransferSpec{
		Direction:  task.Direction,
		Kind:       task.Kind,
		Pod:        task.Pod,
		Container:  task.Container,
		RemotePath: task.RemotePath,
		Size:       task.Size,
		Offset:     task.Offset,
		Checksum:   task.Checksum,
		Overwrite:  task.Overwrite,
		ResumeID:   task.ResumeID,
	}
	if err := validateFileTransferSpec(&spec); err != nil {
		return FileTransferTask{}, errors.New("gateway returned an invalid file transfer Task")
	}
	task.Direction, task.Kind, task.Pod, task.Container = spec.Direction, spec.Kind, spec.Pod, spec.Container
	task.RemotePath, task.Checksum = spec.RemotePath, spec.Checksum
	task.ResumeID = spec.ResumeID
	return task, nil
}
