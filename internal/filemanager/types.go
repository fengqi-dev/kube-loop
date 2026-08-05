package filemanager

import "time"

type Target struct {
	Context   string `json:"context"`
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	PodUID    string `json:"podUID,omitempty"`
	Container string `json:"container"`
}

type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Dir     bool      `json:"dir"`
	Size    int64     `json:"size"`
	Mode    uint32    `json:"mode"`
	ModTime time.Time `json:"modTime"`
}

type Direction string

const (
	DirectionUpload   Direction = "upload"
	DirectionDownload Direction = "download"
)

type TransferRequest struct {
	Direction      Direction `json:"direction"`
	Target         Target    `json:"target"`
	SourcePath     string    `json:"sourcePath"`
	DestinationDir string    `json:"destinationDir"`
	Overwrite      bool      `json:"overwrite"`
}

type TaskStatus string

const (
	StatusQueued    TaskStatus = "queued"
	StatusRunning   TaskStatus = "running"
	StatusPaused    TaskStatus = "paused"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
	StatusCancelled TaskStatus = "cancelled"
	StatusStale     TaskStatus = "stale"
)

type TransferTask struct {
	ID              string     `json:"id"`
	Direction       Direction  `json:"direction"`
	Target          Target     `json:"target"`
	SourcePath      string     `json:"sourcePath"`
	DestinationPath string     `json:"destinationPath"`
	TempPath        string     `json:"tempPath,omitempty"`
	Directory       bool       `json:"directory,omitempty"`
	Status          TaskStatus `json:"status"`
	TotalBytes      int64      `json:"totalBytes"`
	DoneBytes       int64      `json:"doneBytes"`
	SourceModTime   time.Time  `json:"sourceModTime"`
	Overwrite       bool       `json:"overwrite"`
	Error           string     `json:"error,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
}

type persistedState struct {
	Version int            `json:"version"`
	Tasks   []TransferTask `json:"tasks"`
}
