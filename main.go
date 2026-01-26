package main

import (
	"fmt"
	"os"
	"time"

	"gitee.com/EEPPEE_admin/i3statusgo/cmd"
)

var (
	ticker *time.Ticker // 定时刷新器
)

func main() {
	// 1. 启动提示（模拟i3status的输出格式检测）
	fmt.Fprintln(os.Stderr, "i3status-clone: trying to auto-detect output_format setting")
	fmt.Fprintln(os.Stderr, "i3status-clone: falling back to \"none\"")
	fmt.Fprintln(os.Stderr) // 空行

	// 2. 初始化信号处理
	cmd.InitSignalHandler()

	// 3. 预加载首次网络统计（避免首次输出网速为0的延迟）
	_, _, _, _ = cmd.GetNetworkStats()

	// 4. 定时输出状态（2秒刷新）
	ticker = time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// 首次输出
	fmt.Println(cmd.FormatStatusLine())

	// 循环输出
	for range ticker.C {
		fmt.Println(cmd.FormatStatusLine())
	}
}
