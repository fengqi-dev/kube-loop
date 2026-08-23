package relayregistry

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

type TopologyResolver func(context.Context, relaycontrol.PeerIdentity) (map[string]string, error)

type MTLSConfig struct {
	TrustDomain      string
	Namespace        string
	ServiceAccount   string
	TopologyResolver TopologyResolver
}

type MTLSAuthenticator struct{ config MTLSConfig }

func NewMTLSAuthenticator(config MTLSConfig) (*MTLSAuthenticator, error) {
	probe := relaycontrol.PeerIdentity{
		TrustDomain: config.TrustDomain, Namespace: config.Namespace,
		ServiceAccount: config.ServiceAccount, PodUID: "probe",
	}
	if err := probe.Validate(); err != nil {
		return nil, errors.New("relay mTLS identity configuration is invalid")
	}
	return &MTLSAuthenticator{config: config}, nil
}

func (authenticator *MTLSAuthenticator) Authenticate(
	request *http.Request,
) (relaycontrol.PeerIdentity, error) {
	if authenticator == nil || request == nil || request.TLS == nil ||
		len(request.TLS.VerifiedChains) == 0 {
		return relaycontrol.PeerIdentity{}, errors.New(
			"verified Relay client certificate is required",
		)
	}
	identity, err := authenticator.identityFromChains(
		request.TLS.VerifiedChains,
	)
	if err != nil {
		return relaycontrol.PeerIdentity{}, err
	}
	if authenticator.config.TopologyResolver != nil {
		topology, err := authenticator.config.TopologyResolver(
			request.Context(),
			identity,
		)
		if err != nil {
			return relaycontrol.PeerIdentity{}, errors.New(
				"resolve authenticated Relay topology",
			)
		}
		identity.Topology = topology
	}
	if err := identity.Validate(); err != nil {
		return relaycontrol.PeerIdentity{}, err
	}
	return identity, nil
}

func (authenticator *MTLSAuthenticator) identityFromChains(
	chains [][]*x509.Certificate,
) (relaycontrol.PeerIdentity, error) {
	for _, chain := range chains {
		if len(chain) == 0 || chain[0] == nil {
			continue
		}
		for _, identityURI := range chain[0].URIs {
			if identityURI == nil || identityURI.Scheme != "spiffe" ||
				!strings.EqualFold(
					identityURI.Host,
					authenticator.config.TrustDomain,
				) {
				continue
			}
			segments := strings.Split(
				strings.Trim(identityURI.EscapedPath(), "/"),
				"/",
			)
			if len(segments) != 6 || segments[0] != "ns" ||
				segments[2] != "sa" ||
				segments[4] != "pod" {
				continue
			}
			identity := relaycontrol.PeerIdentity{
				TrustDomain: identityURI.Host, Namespace: segments[1],
				ServiceAccount: segments[3], PodUID: segments[5],
			}
			if identity.Namespace != authenticator.config.Namespace ||
				identity.ServiceAccount != authenticator.config.ServiceAccount {
				continue
			}
			if err := identity.Validate(); err == nil {
				return identity, nil
			}
		}
	}
	return relaycontrol.PeerIdentity{}, errors.New(
		"relay client certificate has no allowed workload identity",
	)
}
