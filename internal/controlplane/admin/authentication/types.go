// Package authentication contains the authentication kind shared by
// management sessions and audit records.
package authentication

type Type string

const Normal Type = "normal"

type Subject struct {
	ID             string
	Authentication Type
}
