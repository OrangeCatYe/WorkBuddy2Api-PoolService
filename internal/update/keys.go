package update

import "crypto/ed25519"

// trustedKeys 受信的更新签名公钥（base64）。可以同时放多把：轮换密钥时先把新公钥随一个版本发出去，
// 之后再用新私钥签名，旧版本也能平滑过渡。
//
// 对应的私钥只在发版流水线的 Secret UPDATE_SIGNING_KEY 里，不在仓库中。
var trustedKeys = []string{
	"Hc4Yx97OGCglyI3HyLwtdBWJjuDcsixLWxIsLnDY6Ok=",
}

// TrustedKeys 解码后的内置公钥。
func TrustedKeys() []ed25519.PublicKey {
	keys, _ := ParseKeys(trustedKeys)
	return keys
}
