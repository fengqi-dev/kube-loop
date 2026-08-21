package storage

const (
	identityFilterSQL      = " AND identity_id = ?"
	descendingPageSQL      = " ORDER BY created_at DESC, id DESC LIMIT ?"
	statusActive           = "active"
	auditSourceAPI         = "api"
	grantAuthorizationCode = "authorization_code"
	grantRefreshToken      = "refresh_token"
	scopeOfflineAccess     = "offline_access"
	scopeKubeLoopAPI       = "kubeloop.api"
	scopeOpenID            = "openid"
	scopeProfile           = "profile"
	emailField             = "email"
	statusExpired          = "expired"
	identityTypeHuman      = "human"
	sessionKindNormal      = "normal"
	statusPending          = "pending"
	outcomeSuccess         = "success"
	operatingSystemWindows = "windows"
)
