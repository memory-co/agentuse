//go:build !linux && !darwin

package cli

import "errors"

func execServer(string, []string, []string) error {
	return errors.New("当前平台不支持 Server 自升级重启")
}
