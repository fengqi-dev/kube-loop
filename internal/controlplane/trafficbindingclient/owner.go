package trafficbindingclient

// Owner is the durable business Session owner written directly into a
// TrafficBinding. It deliberately contains no database Task reference.
type Owner struct {
	IdentityID        string
	SessionID         string
	TaskID            string
	SessionGeneration int64
}
