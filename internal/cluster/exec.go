package cluster

import (
	"context"

	"github.com/fengqi-dev/kube-loop/internal/cluster/podexec"
	"github.com/fengqi-dev/kube-loop/internal/podssh"
)

func (p *Provider) Exec(
	ctx context.Context,
	target podssh.Target,
	command []string,
	streams podssh.Streams,
) error {
	config, err := p.RESTConfig(target.Context)
	if err != nil {
		return err
	}
	return podexec.Exec(ctx, config, target, command, streams)
}
