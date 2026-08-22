package fileapi

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

func (handler *Service) normalizeSpec(spec *Spec) *controlplaneapi.Error {
	spec.Direction = strings.TrimSpace(strings.ToLower(spec.Direction))
	spec.Kind = strings.TrimSpace(strings.ToLower(spec.Kind))
	spec.Pod = strings.TrimSpace(spec.Pod)
	spec.Container = strings.TrimSpace(spec.Container)
	spec.Checksum = strings.TrimSpace(strings.ToLower(spec.Checksum))
	spec.ResumeID = strings.TrimSpace(strings.ToLower(spec.ResumeID))
	if spec.Direction != DirectionUpload &&
		spec.Direction != DirectionDownload {
		return invalid("direction", "direction must be upload or download")
	}
	if spec.Kind != KindFile && spec.Kind != KindDirectory {
		return invalid("kind", "kind must be file or directory")
	}
	if len(validation.IsDNS1123Subdomain(spec.Pod)) != 0 {
		return invalid("pod", "Pod name is invalid")
	}
	if spec.Container != "" &&
		len(validation.IsDNS1123Label(spec.Container)) != 0 {
		return invalid("container", "container name is invalid")
	}
	remotePath, err := normalizeRemotePath(
		spec.RemotePath,
		handler.allowedRoots,
	)
	if err != nil {
		return invalid("remotePath", err.Error())
	}
	spec.RemotePath = remotePath
	spec.AllowedRoot = matchingAllowedRoot(remotePath, handler.allowedRoots)
	if spec.AllowedRoot == "" {
		return invalid(
			"remotePath",
			"container path is outside the configured allowed roots",
		)
	}
	if spec.Offset > handler.maximumBytes {
		return invalid("offset", "offset exceeds the configured transfer limit")
	}
	switch {
	case spec.Direction == DirectionUpload:
		if spec.Size == 0 || spec.Size > handler.maximumBytes ||
			spec.Offset > spec.Size {
			return invalid("size", "upload size or offset is invalid")
		}
		if _, err := filestream.ParseChecksum(spec.Checksum); err != nil {
			return invalid("checksum", err.Error())
		}
		if spec.Kind == KindDirectory && spec.Offset != 0 {
			return invalid(
				"offset",
				"directory upload cannot resume from a byte offset",
			)
		}
		if spec.ResumeID != "" {
			if spec.Kind != KindFile {
				return invalid(
					"resumeId",
					"only file uploads support a Resume ID",
				)
			}
			if _, err := uuid.Parse(spec.ResumeID); err != nil {
				return invalid("resumeId", "upload Resume ID is invalid")
			}
		}
	case spec.Size != 0 || spec.Checksum != "" || spec.Overwrite:
		return invalid("direction", "download metadata is determined by the Gateway")
	case spec.Kind == KindDirectory && spec.Offset != 0:
		return invalid("offset", "directory download cannot resume from a byte offset")
	case spec.ResumeID != "":
		return invalid("resumeId", "downloads do not accept a Resume ID")
	}
	return nil
}
func normalizeRoots(values []string) ([]string, error) {
	if len(values) == 0 {
		return []string{"/"}, nil
	}
	roots := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
		if value == "/" {
			roots = append(roots, value)
			continue
		}
		root, err := normalizeRemotePath(value, []string{"/"})
		if err != nil {
			return nil, fmt.Errorf(
				"file transfer allowed root is invalid: %w",
				err,
			)
		}
		roots = append(roots, root)
	}
	slices.Sort(roots)
	return slices.Compact(roots), nil
}

func normalizeRemotePath(value string, allowedRoots []string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || len(value) > 4096 || !path.IsAbs(value) {
		return "", errors.New(
			"container path must be an absolute path of at most 4096 bytes",
		)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return "", errors.New("container path contains control characters")
		}
	}
	for component := range strings.SplitSeq(value, "/") {
		if component == "." || component == ".." {
			return "", errors.New("container path traversal is not allowed")
		}
	}
	cleaned := path.Clean(value)
	if cleaned == "/" {
		return "", errors.New("container root cannot be transferred")
	}
	if matchingAllowedRoot(cleaned, allowedRoots) != "" {
		return cleaned, nil
	}
	return "", errors.New(
		"container path is outside the configured allowed roots",
	)
}

func matchingAllowedRoot(value string, allowedRoots []string) string {
	matched := ""
	for _, root := range allowedRoots {
		if (root == "/" || value == root || strings.HasPrefix(value, root+"/")) &&
			len(root) > len(matched) {
			matched = root
		}
	}
	return matched
}

// NormalizeAllowedRoots validates and canonicalizes the container roots shared
// by transfer and directory-management APIs.
func NormalizeAllowedRoots(
	values []string,
) ([]string, error) {
	return normalizeRoots(values)
}

// NormalizeContainerPath applies the common lexical path policy and returns
// the most-specific configured root that contains the path.
func NormalizeContainerPath(
	value string,
	allowedRoots []string,
) (string, string, error) {
	if strings.TrimSpace(strings.ReplaceAll(value, "\\", "/")) == "/" &&
		slices.Contains(allowedRoots, "/") {
		return "/", "/", nil
	}
	normalized, err := normalizeRemotePath(value, allowedRoots)
	if err != nil {
		return "", "", err
	}
	root := matchingAllowedRoot(normalized, allowedRoots)
	if root == "" {
		return "", "", errors.New(
			"container path is outside the configured allowed roots",
		)
	}
	return normalized, root, nil
}
