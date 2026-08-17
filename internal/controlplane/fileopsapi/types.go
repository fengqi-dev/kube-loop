package fileopsapi

import "time"

type Spec struct {
	Action          string `json:"action"`
	Pod             string `json:"pod"`
	Container       string `json:"container,omitempty"`
	Path            string `json:"path"`
	Destination     string `json:"destination,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Recursive       bool   `json:"recursive,omitempty"`
	AllowedRoot     string `json:"-"`
	DestinationRoot string `json:"-"`
}

type Entry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Kind       string    `json:"kind"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

type Result struct {
	Completed bool   `json:"completed"`
	Error     string `json:"error,omitempty"`
}
