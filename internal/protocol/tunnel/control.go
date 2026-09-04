package tunnel

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

// WriteAuthorizedControlSession registers the immutable NetworkSpec snapshot
// for a RelayTicket-bound Session.
func WriteAuthorizedControlSession(w io.Writer, token SessionToken, spec networkspec.Spec) error {
	contents, err := networkspec.CanonicalJSON(spec)
	if err != nil {
		return err
	}
	value, err := appendSessionHeader(make([]byte, 0, 41+4+len(contents)), CommandControl, token)
	if err != nil {
		return err
	}
	// Canonical NetworkSpec JSON is bounded by networkspec.MaximumJSONSize.
	value = binary.BigEndian.AppendUint32(value, uint32(len(contents))) //nolint:gosec // NetworkSpec JSON is bounded.
	value = append(value, contents...)
	return utils.WriteAll(w, value)
}

func ReadAuthorizedControlSpec(r io.Reader) (networkspec.Spec, error) {
	var sizeRaw [4]byte
	if _, err := io.ReadFull(r, sizeRaw[:]); err != nil {
		return networkspec.Spec{}, err
	}
	size := int(binary.BigEndian.Uint32(sizeRaw[:]))
	if size == 0 || size > networkspec.MaximumJSONSize {
		return networkspec.Spec{}, errors.New("authorized NetworkSpec size is invalid")
	}
	contents := make([]byte, size)
	if _, err := io.ReadFull(r, contents); err != nil {
		return networkspec.Spec{}, err
	}
	return networkspec.Decode(contents)
}
