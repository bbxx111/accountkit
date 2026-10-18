// Package gointegration 提供直接调用 accountkit 的 Go 示例。
//
// example_test.go 中带 Output 的示例只运行合成数据，不连接数据库或发送验证码。
// 其余示例是可编译的接入片段：调用前须用宿主已经完成迁移和启动的 Auth、
// 已校验输入及可信元数据替换声明处的变量；这些片段不作为独立程序执行。
// 完整装配与运行步骤见 docs/go-integration.md，开发 HTTP 宿主见 examples/embedded。
package gointegration
