package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// DecodeStrictJSON unmarshals exactly one JSON value from raw into destination.
// Unknown fields and trailing data are rejected so a peer cannot smuggle extra
// content past a document that otherwise validates.
func DecodeStrictJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}
