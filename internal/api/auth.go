package api

import (
	"crypto/md5"
	"encoding/hex"
	"time"
)

func token(password string) (string, string) {
	salt := hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	sum := md5.Sum([]byte(password + salt))
	return hex.EncodeToString(sum[:]), salt
}
