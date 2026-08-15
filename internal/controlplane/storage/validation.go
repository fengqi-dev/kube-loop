package storage

import "regexp"

const ManagementActorBreakGlass = "break-glass"

var dns1123Label = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
