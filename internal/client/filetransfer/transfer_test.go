package filetransfer

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/testutil/websockettest"
)

type testClient struct{ endpoint string }

func (client testClient) CreateFileTransferTask(
	_ context.Context,
	_ profile.Profile,
	session remote.Session,
	spec remote.FileTransferSpec,
	_ string,
) (remote.FileTransferTask, error) {
	now := time.Now().UTC()
	return remote.FileTransferTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: "pending",
		Direction: spec.Direction, Kind: spec.Kind, Pod: spec.Pod, Container: spec.Container,
		RemotePath: spec.RemotePath, Size: spec.Size, Offset: spec.Offset, Checksum: spec.Checksum,
		Overwrite: spec.Overwrite, ResumeID: spec.ResumeID,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}, nil
}

func (client testClient) OpenFileTransferStream(
	ctx context.Context,
	_ profile.Profile,
	_ remote.Session,
	_ remote.FileTransferTask,
) (*websocket.Conn, error) {
	connection, _, err := websockettest.Dial(ctx, client.endpoint, nil)
	return connection, err
}

func websocketURL(serverURL string) string { return "ws" + strings.TrimPrefix(serverURL, "http") }
