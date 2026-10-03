# 固定源基线

来源：ai-food 提交 `7b4c4ecfdba4a05d54810aa2152b40d5c7da00c2` 的 `packages/auth-server`。
`manifest.json` 记录由 git cat-file blob 读取的 130 个源文件原始 Git blob 字节的 SHA-256（不经过 Windows checkout 或 archive 的换行转换）；两个 0001 SQL 为原始字节副本。

fixture.json 完全为合成数据，不包含真实用户或密钥。compatibility_test.go 使用标准库按照源格式构造 nonce||AES-GCM、HMAC-SHA256 摘要、SHA-256 refresh 哈希及 HS256 JWT；时间相对测试运行时生成以避免夹具过期，不调用 accountkit 的签发/加密实现来生成预期值。