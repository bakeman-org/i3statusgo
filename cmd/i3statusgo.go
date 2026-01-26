package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath" // 新增：用于拼接文件路径
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"

	// 给gopsutil的net包起别名，解决和标准库net的冲突
	netstat "github.com/shirou/gopsutil/v3/net"
	// 标准库net保留，用于获取IP
	"net"
)

// 网络状态缓存（用于计算网速）
type netStats struct {
	recvBytes uint64
	sendBytes uint64
	timestamp time.Time
}

var (
	lastNetStats netStats                  // 上一次网络统计数据
	ticker       *time.Ticker              // 定时刷新器
	quitChan     = make(chan os.Signal, 1) // 信号通道
	// 预编译正则表达式，解析nmcli输出
	signalRegex = regexp.MustCompile(`signal:\s*(\d+)`)
)

// BytesToHuman 字节转人类可读格式（KB/GiB等）
func BytesToHuman(b uint64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)

	switch {
	case b >= TB:
		return fmt.Sprintf("%.2f TiB", float64(b)/TB)
	case b >= GB:
		return fmt.Sprintf("%.2f GiB", float64(b)/GB)
	case b >= MB:
		return fmt.Sprintf("%.2f MiB", float64(b)/MB)
	case b >= KB:
		return fmt.Sprintf("%.2f KB", float64(b)/KB)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// BytesPerSecToHuman 字节/秒转人类可读格式（KB/s等）
func BytesPerSecToHuman(b float64) string {
	const (
		KB = 1024
		MB = KB * 1024
	)

	switch {
	case b >= MB:
		return fmt.Sprintf("%.2f MB/s", b/MB)
	case b >= KB:
		return fmt.Sprintf("%.2f KB/s", b/KB)
	default:
		return fmt.Sprintf("%.0f B/s", b)
	}
}

// GetLocalIP 获取本地非回环IPv4地址（优先活跃网络接口）
func GetLocalIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}

	for _, iface := range ifaces {
		// 跳过未启动、回环、虚拟接口
		if iface.Flags&net.FlagUp == 0 ||
			iface.Flags&net.FlagLoopback != 0 ||
			strings.HasPrefix(iface.Name, "docker") ||
			strings.HasPrefix(iface.Name, "veth") {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() {
				continue
			}

			if ip4 := ipNet.IP.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return "127.0.0.1"
}

// GetWiFiInfo 通过nmcli获取真实的WiFi名称和信号强度（修复TYPE匹配问题）
// 返回：(WiFi名称, 信号百分比, 错误)
func GetWiFiInfo() (string, string, error) {
	// 1. 检查nmcli是否存在
	if _, err := exec.LookPath("nmcli"); err != nil {
		return "unknown", "0%", fmt.Errorf("nmcli not found: %w", err)
	}

	// 2. 获取当前活跃的WiFi连接
	activeConnCmd := exec.Command("nmcli", "-t", "-f", "NAME,TYPE", "connection", "show", "--active")
	connOutput, err := activeConnCmd.CombinedOutput()
	if err != nil {
		return "unknown", "0%", fmt.Errorf("get active connections: %w (output: %s)", err, string(connOutput))
	}

	// 解析活跃连接，找到WiFi类型的连接名称（核心修复点）
	var wifiConnName string
	lines := strings.Split(strings.TrimSpace(string(connOutput)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, ":")
		// 确保分割后有至少2个部分，且过滤掉loopback
		if len(parts) < 2 || strings.Contains(strings.ToLower(parts[1]), "loopback") {
			continue
		}

		connName := strings.TrimSpace(parts[0])
		connType := strings.TrimSpace(parts[1])

		// 兼容多种WiFi TYPE值：wifi / 802-11-wireless / 802.11.wireless
		if strings.Contains(strings.ToLower(connType), "wifi") ||
			strings.Contains(strings.ToLower(connType), "wireless") {
			wifiConnName = connName
			break // 找到第一个WiFi连接即可
		}
	}

	if wifiConnName == "" {
		return "disconnected", "0%", nil
	}

	// 3. 获取该WiFi的详细信息（信号强度）
	// 修复：使用dev status获取当前连接的WiFi设备，再查信号
	devStatusCmd := exec.Command("nmcli", "-t", "-f", "DEVICE,TYPE,STATE", "dev", "status")
	devOutput, err := devStatusCmd.CombinedOutput()
	if err != nil {
		return GetWiFiInfoFallback(wifiConnName)
	}

	// 找到WiFi对应的设备名
	var wifiDevice string
	devLines := strings.Split(strings.TrimSpace(string(devOutput)), "\n")
	for _, line := range devLines {
		parts := strings.Split(line, ":")
		if len(parts) >= 3 &&
			(strings.Contains(strings.ToLower(parts[1]), "wifi") || strings.Contains(strings.ToLower(parts[1]), "wireless")) &&
			strings.Contains(strings.ToLower(parts[2]), "connected") {
			wifiDevice = strings.TrimSpace(parts[0])
			break
		}
	}

	// 通过设备名获取信号强度（更可靠）
	var signalVal string
	if wifiDevice != "" {
		wifiSignalCmd := exec.Command("nmcli", "-t", "-f", "SIGNAL", "dev", "wifi", "show", wifiDevice)
		signalOutput, err := wifiSignalCmd.CombinedOutput()
		if err == nil {
			signalVal = strings.TrimSpace(string(signalOutput))
		}
	}

	// 备用解析：如果直接获取失败，用正则匹配
	if signalVal == "" {
		wifiDetailCmd := exec.Command("nmcli", "dev", "wifi", "list", "ifname", wifiDevice)
		detailOutput, err := wifiDetailCmd.CombinedOutput()
		if err == nil {
			matches := signalRegex.FindStringSubmatch(string(detailOutput))
			if len(matches) >= 2 {
				signalVal = matches[1]
			}
		}
	}

	// 最终容错
	if signalVal == "" {
		return GetWiFiInfoFallback(wifiConnName)
	}

	return wifiConnName, fmt.Sprintf("%s%%", signalVal), nil
}

// GetWiFiInfoFallback 备用方案：通过nmcli dev wifi获取信号（兼容不同nmcli版本）
func GetWiFiInfoFallback(ssid string) (string, string, error) {
	wifiListCmd := exec.Command("nmcli", "-t", "-f", "SSID,SIGNAL", "dev", "wifi")
	output, err := wifiListCmd.CombinedOutput()
	if err != nil {
		return ssid, "0%", fmt.Errorf("fallback get signal: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		parts := strings.Split(line, ":")
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == ssid {
			signal := strings.TrimSpace(parts[1])
			return ssid, fmt.Sprintf("%s%%", signal), nil
		}
	}

	return ssid, "0%", nil
}

// GetNetworkStats 获取真实网络状态（名称、信号、网速）- 生产级实现
func GetNetworkStats() (name string, signal string, downSpeed, upSpeed string) {
	// 1. 获取真实WiFi信息
	wifiName, wifiSignal, err := GetWiFiInfo()

	if err != nil {
		// 容错：记录错误但不崩溃，使用默认值
		fmt.Fprintf(os.Stderr, "WARN: get wifi info failed: %v\n", err)
		name = "unknown"
		signal = "0%"
	} else {
		name = wifiName
		signal = wifiSignal
	}

	// 2. 获取真实网络流量（解决net包冲突后）
	netIOList, err := netstat.IOCounters(true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: get net io failed: %v\n", err)
		return name, signal, "0 B/s", "0 B/s"
	}

	// 遍历接口，获取非回环、非虚拟接口的总流量
	var currRecv, currSend uint64
	for _, io := range netIOList {
		// 跳过回环、docker、虚拟接口
		if io.Name == "lo" ||
			strings.HasPrefix(io.Name, "docker") ||
			strings.HasPrefix(io.Name, "veth") ||
			strings.HasPrefix(io.Name, "br-") {
			continue
		}
		currRecv += io.BytesRecv
		currSend += io.BytesSent
	}

	// 3. 计算网速
	now := time.Now()
	if lastNetStats.timestamp.IsZero() {
		lastNetStats = netStats{recvBytes: currRecv, sendBytes: currSend, timestamp: now}
		return name, signal, "0 B/s", "0 B/s"
	}

	// 时间差（秒）
	deltaTime := now.Sub(lastNetStats.timestamp).Seconds()
	if deltaTime <= 0 {
		deltaTime = 1
	}

	// 流量差
	deltaRecv := float64(currRecv - lastNetStats.recvBytes)
	deltaSend := float64(currSend - lastNetStats.sendBytes)

	// 转换为人类可读格式
	downSpeed = BytesPerSecToHuman(deltaRecv / deltaTime)
	upSpeed = BytesPerSecToHuman(deltaSend / deltaTime)

	// 更新缓存
	lastNetStats = netStats{recvBytes: currRecv, sendBytes: currSend, timestamp: now}

	return name, signal, downSpeed, upSpeed
}

// GetDiskStats 获取磁盘总容量和使用率（根分区）- 生产级容错
func GetDiskStats() (total string, usage string) {
	stats, err := disk.Usage("/")
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: get disk usage failed: %v\n", err)
		return "0 GiB", "0.00%"
	}

	total = BytesToHuman(stats.Total)
	usage = fmt.Sprintf("%.2f%%", stats.UsedPercent)
	return
}

// GetCPUUsage 获取CPU使用率（优化：非阻塞采样）
func GetCPUUsage() string {
	// 异步采样，避免阻塞主循环
	percentChan := make(chan float64, 1)
	go func() {
		percent, err := cpu.Percent(0, false) // 0秒采样（获取累计使用率）
		if err != nil || len(percent) == 0 {
			percentChan <- 0.0
			return
		}
		percentChan <- percent[0]
	}()

	// 超时保护
	select {
	case percent := <-percentChan:
		return fmt.Sprintf("%.2f", percent)
	case <-time.After(500 * time.Millisecond):
		return "0.00"
	}
}

// GetMemStats 获取内存和交换分区使用（总容量）- 生产级容错
func GetMemStats() (memTotal string, swapTotal string) {
	// 内存
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: get mem info failed: %v\n", err)
		memTotal = "0 GiB"
	} else {
		memTotal = BytesToHuman(memInfo.Total)
	}

	// 交换分区
	swapInfo, err := mem.SwapMemory()
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: get swap info failed: %v\n", err)
		swapTotal = "0 GiB"
	} else {
		swapTotal = BytesToHuman(swapInfo.Total)
	}

	return
}

// GetBatteryInfo 增强版：获取电池状态、百分比、剩余能量、满电能量、实时功率
// 返回：(充电状态, 电量百分比, 剩余能量(Wh), 满电能量(Wh), 实时功率(W), 错误)
func GetBatteryInfo() (string, string, string, string, string, error) {
	// Linux系统电池信息的标准路径
	powerSupplyDir := "/sys/class/power_supply/"

	// 优先检查BAT0（最常见的电池设备）
	batteryDir := filepath.Join(powerSupplyDir, "BAT0")
	if fi, err := os.Stat(batteryDir); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "WARN: BAT0 not found, try to scan all BAT devices\n")
		dirEntries, err := os.ReadDir(powerSupplyDir)
		if err != nil {
			return "N/A", "0%", "0 Wh", "0 Wh", "0 W", fmt.Errorf("read power supply dir failed: %w (path: %s)", err, powerSupplyDir)
		}

		// 调试：输出遍历到的所有目录
		var foundDirs []string
		for _, entry := range dirEntries {
			foundDirs = append(foundDirs, entry.Name())
		}
		fmt.Fprintf(os.Stderr, "DEBUG: power_supply dir entries: %v\n", foundDirs)

		// 遍历查找BAT开头的电池目录（兼容大小写）
		var batDirFound string
		for _, entry := range dirEntries {
			if entry.IsDir() && strings.HasPrefix(strings.ToUpper(entry.Name()), "BAT") {
				batDirFound = filepath.Join(powerSupplyDir, entry.Name())
				fmt.Fprintf(os.Stderr, "DEBUG: found battery dir: %s\n", batDirFound)
				break
			}
		}

		if batDirFound == "" {
			return "No battery", "0%", "0 Wh", "0 Wh", "0 W", nil
		}
		batteryDir = batDirFound
	}

	// 确认电池目录存在
	if _, err := os.Stat(batteryDir); err != nil {
		return "No battery", "0%", "0 Wh", "0 Wh", "0 W", fmt.Errorf("battery dir not exist: %w (path: %s)", err, batteryDir)
	}

	// 1. 读取电量百分比（原有逻辑）
	capacityPath := filepath.Join(batteryDir, "capacity")
	capacityBytes, err := os.ReadFile(capacityPath)
	if err != nil {
		return "Error", "0%", "0 Wh", "0 Wh", "0 W", fmt.Errorf("read capacity failed: %w (path: %s)", err, capacityPath)
	}
	capacity := strings.TrimSpace(string(capacityBytes))

	// 2. 读取充电状态（原有逻辑）
	statusPath := filepath.Join(batteryDir, "status")
	statusBytes, err := os.ReadFile(statusPath)
	if err != nil {
		return "Error", capacity + "%", "0 Wh", "0 Wh", "0 W", fmt.Errorf("read status failed: %w (path: %s)", err, statusPath)
	}
	status := strings.TrimSpace(string(statusBytes))
	// 标准化状态
	switch strings.ToLower(status) {
	case "charging":
		status = "Charging"
	case "discharging":
		status = "Discharging"
	case "full":
		status = "Full"
	case "not charging":
		status = "Not charging"
	default:
		status = fmt.Sprintf("Unknown(%s)", status)
	}

	// 3. 读取当前剩余能量（energy_now，μWh → 转换为Wh）
	energyNowPath := filepath.Join(batteryDir, "energy_now")
	energyNowStr := "0"
	var energyNowWh float64
	energyNowBytes, err := os.ReadFile(energyNowPath)
	if err == nil {
		energyNowStr = strings.TrimSpace(string(energyNowBytes))
		// 转换：μWh → Wh（除以1e6）
		energyNowInt, _ := strconv.ParseInt(energyNowStr, 10, 64)
		energyNowWh = float64(energyNowInt) / 1000000.0
	} else {
		fmt.Fprintf(os.Stderr, "WARN: read energy_now failed: %v (path: %s)\n", err, energyNowPath)
	}
	energyNowDisplay := fmt.Sprintf("%.2f Wh", energyNowWh)

	// 4. 读取满电能量（energy_full，μWh → 转换为Wh）
	energyFullPath := filepath.Join(batteryDir, "energy_full")
	energyFullStr := "0"
	var energyFullWh float64
	energyFullBytes, err := os.ReadFile(energyFullPath)
	if err == nil {
		energyFullStr = strings.TrimSpace(string(energyFullBytes))
		energyFullInt, _ := strconv.ParseInt(energyFullStr, 10, 64)
		energyFullWh = float64(energyFullInt) / 1000000.0
	} else {
		fmt.Fprintf(os.Stderr, "WARN: read energy_full failed: %v (path: %s)\n", err, energyFullPath)
	}
	energyFullDisplay := fmt.Sprintf("%.2f Wh", energyFullWh)

	// 5. 读取实时功率（power_now，μW → 转换为W，充电为正，放电为负）
	powerNowPath := filepath.Join(batteryDir, "power_now")
	powerNowStr := "0"
	var powerNowW float64
	powerNowBytes, err := os.ReadFile(powerNowPath)
	if err == nil {
		powerNowStr = strings.TrimSpace(string(powerNowBytes))
		powerNowInt, _ := strconv.ParseInt(powerNowStr, 10, 64)
		powerNowW = float64(powerNowInt) / 1000000.0
	} else {
		fmt.Fprintf(os.Stderr, "WARN: read power_now failed: %v (path: %s)\n", err, powerNowPath)
	}
	// 格式化功率显示（充电+W，放电-W）
	powerNowDisplay := fmt.Sprintf("%.2f W", powerNowW)

	return status, capacity + "%", energyNowDisplay, energyFullDisplay, powerNowDisplay, nil
}

// FormatStatusLine 格式化输出行（含完整电池实时信息）
func FormatStatusLine() string {
	// 1. 网络信息
	netName, signal, downSpeed, upSpeed := GetNetworkStats()
	ip := GetLocalIP()

	// 2. 磁盘信息
	diskTotal, diskUsage := GetDiskStats()

	// 3. CPU使用率
	cpuUsage := GetCPUUsage()

	// 4. 内存/交换分区
	memTotal, swapTotal := GetMemStats()

	// 5. 电池信息（增强版）
	batteryStatus, batteryCapacity, batteryNow, batteryFull, batteryPower, err := GetBatteryInfo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: get battery info failed: %v\n", err)
		batteryStatus = "Error"
		batteryCapacity = "0%"
		batteryNow = "0.00 Wh"
		batteryFull = "0.00 Wh"
		batteryPower = "0.00 W"
	}

	// 6. 当前时间
	now := time.Now().Format("2006-01-02 15:04:05")

	// 拼接格式（新增电池实时能量/功率）
	line := fmt.Sprintf(
		"W: ( %s at %s) %s |S ↓ %s ↑ %s | Disk Used %s | Full %s |C: %s |M: %s |S: %s | B: %s (%s, %s/%s, %s) | %s",
		signal, netName, ip,
		downSpeed, upSpeed,
		diskUsage, diskTotal,
		cpuUsage, memTotal, swapTotal,
		batteryStatus, batteryCapacity, batteryNow, batteryFull, batteryPower,
		now,
	)
	return line
}

// InitSignalHandler 初始化信号处理（捕获Ctrl+C）
func InitSignalHandler() {
	signal.Notify(quitChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		<-quitChan
		fmt.Println("\ni3status-clone: exiting due to signal.")
		if ticker != nil {
			ticker.Stop()
		}
		os.Exit(0)
	}()
}

func main() {
	// 1. 启动提示（模拟i3status的输出格式检测）
	fmt.Fprintln(os.Stderr, "i3status-clone: trying to auto-detect output_format setting")
	fmt.Fprintln(os.Stderr, "i3status-clone: falling back to \"none\"")
	fmt.Fprintln(os.Stderr) // 空行

	// 2. 初始化信号处理
	InitSignalHandler()

	// 3. 预加载首次网络统计（避免首次输出网速为0的延迟）
	_, _, _, _ = GetNetworkStats()

	// 4. 定时输出状态（2秒刷新）
	ticker = time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// 首次输出
	fmt.Println(FormatStatusLine())

	// 循环输出
	for range ticker.C {
		fmt.Println(FormatStatusLine())
	}
}
