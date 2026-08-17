package supervisor

// Version selects the isolated dev or release supervisor channel. Release
// builds override it at application and supervisor composition roots.
var Version = "dev"
