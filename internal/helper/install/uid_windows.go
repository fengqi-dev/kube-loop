//go:build windows

package install

import "golang.org/x/sys/windows"

func currentUID() int { return 0 }

func currentUserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}
