package authentication

type Type string

const Normal Type = "normal"

type Subject struct {
	ID             string
	Authentication Type
}
