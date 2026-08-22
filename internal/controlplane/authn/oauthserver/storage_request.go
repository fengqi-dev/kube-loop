package oauthserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/ory/fosite"
)

type requestDTO struct {
	ID                string     `json:"id"`
	RequestedAt       time.Time  `json:"requested_at"`
	ClientID          string     `json:"client_id"`
	RequestedScopes   []string   `json:"requested_scopes"`
	GrantedScopes     []string   `json:"granted_scopes"`
	RequestedAudience []string   `json:"requested_audience"`
	GrantedAudience   []string   `json:"granted_audience"`
	Form              url.Values `json:"form"`
	Session           *Session   `json:"session"`
}

func encodeRequester(request fosite.Requester) (json.RawMessage, error) {
	session, ok := request.GetSession().(*Session)
	if !ok {
		return nil, errors.New("oauth request session has an invalid type")
	}
	return json.Marshal(
		requestDTO{
			ID:                request.GetID(),
			RequestedAt:       request.GetRequestedAt(),
			ClientID:          request.GetClient().GetID(),
			RequestedScopes:   request.GetRequestedScopes(),
			GrantedScopes:     request.GetGrantedScopes(),
			RequestedAudience: request.GetRequestedAudience(),
			GrantedAudience:   request.GetGrantedAudience(),
			Form:              request.GetRequestForm(),
			Session:           session,
		},
	)
}

func (storage *Storage) decodeRequester(
	ctx context.Context,
	raw json.RawMessage,
) (fosite.Requester, error) {
	var dto requestDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, errors.New("decode OAuth request")
	}
	client, err := storage.GetClient(ctx, dto.ClientID)
	if err != nil {
		return nil, err
	}
	request := fosite.NewRequest()
	request.ID = dto.ID
	request.RequestedAt = dto.RequestedAt
	request.Client = client
	request.RequestedScope = dto.RequestedScopes
	request.GrantedScope = dto.GrantedScopes
	request.RequestedAudience = dto.RequestedAudience
	request.GrantedAudience = dto.GrantedAudience
	request.Form = dto.Form
	if request.Form == nil {
		request.Form = url.Values{}
	}
	request.Session = dto.Session
	if request.Session == nil {
		request.Session = NewSession()
	}
	return request, nil
}

func signatureHash(
	signature string,
) []byte {
	sum := sha256.Sum256([]byte(signature))
	return sum[:]
}
