package encrypt

import (
	"github.com/scut2024hjt/convo/settings"
	"crypto/md5"
	"encoding/hex"
)

func EncryptPassword(opassword string) string {
	h := md5.New()
	h.Write([]byte(settings.Conf.EncryptConfig.SecretKey))
	return hex.EncodeToString(h.Sum([]byte(opassword)))
}
