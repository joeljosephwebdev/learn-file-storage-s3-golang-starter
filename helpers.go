package main

import (
	"crypto/rand"
	"encoding/base64"
)

func CreateFileID() string {
	key := make([]byte, 32)
	rand.Read(key)
	base64String := base64.RawURLEncoding.EncodeToString(key)
	return base64String
}
