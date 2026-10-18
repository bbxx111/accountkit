package main

import (
	"net/http"

	"github.com/bbxx111/accountkit"
	akprom "github.com/bbxx111/accountkit/observability/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// 本示例没有 Output，仅编译以核对真实宿主的公开装配接口；不监听或执行认证。
func Example_hostObserver() {
	var cfg accountkit.Config // 替换为宿主已准备的密钥、身份策略及实例隔离配置。
	var deps accountkit.Deps  // 替换为宿主拥有的 Pool、Redis、发送器及其他依赖。
	registry := prometheus.NewRegistry()
	observer, err := akprom.New(registry, "consumer-auth")
	if err != nil {
		return // 宿主安全记录注册错误并停止启动，不复用冲突 collectors。
	}
	deps.Observer = observer
	auth, err := accountkit.New(cfg, deps) // New 只校验和装配，不连接外部依赖。
	if err != nil {
		return
	}
	defer func() {
		auth.Close() // 不注销宿主 collectors，也不关闭共享 Registry。
		// 宿主随后排空外部观测生产者并完成最终采集；Registry 无 Shutdown。
	}()

	// 宿主将该路由挂到有访问控制的管理网络，自行配置监听地址、TLS 和认证。
	// 此处仅创建 handler，没有启动 listener。
	hostAdminMux := http.NewServeMux()
	hostAdminMux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	// 真实宿主接下来执行 Migrate、Start，再接入 HTTP/RPC 请求。
	// 退出前先停止接入并排空全部业务调用，再执行 auth.Close 和最终采集。
}
