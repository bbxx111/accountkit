// Package httpflows 以 httptest 挂载内置终端用户 HTTP 面，走完客户端必须处理的协议层流程：
// 首次登录与 pending 补全、刷新协调与宽限、宽限外重放、重新认证、注销/撤销/解绑导致的退出。
// 它是 docs/http-client-flows.md 的可执行对照，只断言状态机与响应形状，不断言随机凭证值。
package httpflows
