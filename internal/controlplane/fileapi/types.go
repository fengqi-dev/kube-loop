package fileapi

type Spec struct {
	Direction   string `json:"direction"`
	Kind        string `json:"kind"`
	Pod         string `json:"pod"`
	Container   string `json:"container,omitempty"`
	RemotePath  string `json:"remotePath"`
	Size        uint64 `json:"size,omitempty"`
	Offset      uint64 `json:"offset,omitempty"`
	Checksum    string `json:"checksum,omitempty"`
	Overwrite   bool   `json:"overwrite,omitempty"`
	ResumeID    string `json:"resumeId,omitempty"`
	AllowedRoot string `json:"-"`
}
