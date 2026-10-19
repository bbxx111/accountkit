package main

import (
	"context"

	"github.com/bbxx111/accountkit"
	akotel "github.com/bbxx111/accountkit/observability/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// 本示例没有 Output，仅编译以核对真实宿主的公开装配接口；不执行认证或依赖 I/O。
func Example_hostObserver() {
	var cfg accountkit.Config // 替换为宿主已准备的密钥、身份策略及实例隔离配置。
	var deps accountkit.Deps  // 替换为宿主拥有的 Pool、Redis、发送器及其他依赖。
	ctx := context.Background()

	// 由宿主选择 Reader/Exporter。这里使用本地 Reader，不启动网络导出。
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	// 共享 Provider 时，实际宿主须等所有生产者结束后才执行最终刷新和关闭。
	defer func() {
		_ = provider.ForceFlush(ctx)
		_ = provider.Shutdown(ctx)
	}()

	observer, err := akotel.New(provider, "consumer-auth")
	if err != nil {
		return // 宿主安全记录配置失败并停止启动。
	}
	deps.Observer = observer
	auth, err := accountkit.New(cfg, deps) // New 只校验和装配，不连接外部依赖。
	if err != nil {
		return // 上面的 defer 仍会关闭本示例创建的 Provider。
	}
	defer func() {
		auth.Close() // 先停止库维护和自建审计，不关闭宿主 Provider。
		// 宿主此后排空仍会产生观测的外部审计等组件，再进入最终刷新/关闭。
	}()

	// 真实宿主接下来执行 Migrate、Start，再接入 HTTP/RPC 请求。
	// 退出前先停止接入并排空全部业务调用，再按上述顺序收尾。
}
