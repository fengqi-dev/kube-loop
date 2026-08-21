package supervisor

// Version selects the isolated dev or release supervisor channel for clients.
// The supervisor executable receives its channel explicitly at runtime because
// its binary version is independent from the application release.
var Version = "dev"
