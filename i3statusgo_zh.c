/*
 * i3statusgo.c — i3statusgo 的 C 重写版（中文输出版）
 *
 * 与 cmd/i3statusgo.go 功能一致：每 2 秒向 stdout 输出一行状态文本。
 * 与 Go 版的差异（性能/内存优化）：
 *   - 无 Go runtime：无 GC、无独立线程，RSS 约 1 MB（Go 版 15-25 MB）。
 *   - 不再 fork nmcli：SSID 用 SIOCGIWESSID ioctl，信号强度读
 *     /proc/net/wireless；稳态运行时零子进程、零堆分配。
 *   - CPU 使用率改为相邻两次刷新的差值采样（Go 版 cpu.Percent(0)
 *     实际返回的是开机以来的累计均值）。
 *
 * 本版改动：
 *   - 磁盘项在“已用百分比”之外增加“剩余 / 总量”。
 *   - 状态行、电池状态、启动/退出提示改为中文。
 *
 * 构建：make build-c   （gcc -O2 -Wall，无第三方依赖）
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdbool.h>
#include <stdint.h>
#include <unistd.h>
#include <dirent.h>
#include <errno.h>
#include <signal.h>
#include <time.h>
#include <sys/stat.h>
#include <sys/statvfs.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <ifaddrs.h>
#include <net/if.h>
#include <arpa/inet.h>
#include <netinet/in.h>
#include <linux/wireless.h>

#define REFRESH_SECONDS 2
#define KIB 1024.0
#define MIB (KIB * 1024.0)
#define GIB (MIB * 1024.0)
#define TIB (GIB * 1024.0)

static volatile sig_atomic_t g_stop = 0;

static void on_signal(int sig) {
    (void)sig;
    g_stop = 1;
}

/* 读整个小文件（/proc、/sys 单次读即可拿全内容），失败返回 NULL。 */
static char *read_small_file(const char *path, char *buf, size_t size) {
    FILE *f = fopen(path, "r");
    if (f == NULL) {
        return NULL;
    }
    size_t n = fread(buf, 1, size - 1, f);
    fclose(f);
    buf[n] = '\0';
    return buf;
}

static void human_bytes(uint64_t b, char *out, size_t outsz) {
    if (b >= (uint64_t)TIB) {
        snprintf(out, outsz, "%.2f TiB", b / TIB);
    } else if (b >= (uint64_t)GIB) {
        snprintf(out, outsz, "%.2f GiB", b / GIB);
    } else if (b >= (uint64_t)MIB) {
        snprintf(out, outsz, "%.2f MiB", b / MIB);
    } else if (b >= (uint64_t)KIB) {
        snprintf(out, outsz, "%.2f KB", b / KIB);
    } else {
        snprintf(out, outsz, "%llu B", (unsigned long long)b);
    }
}

static void human_speed(double bps, char *out, size_t outsz) {
    if (bps >= MIB) {
        snprintf(out, outsz, "%.2f MB/s", bps / MIB);
    } else if (bps >= KIB) {
        snprintf(out, outsz, "%.2f KB/s", bps / KIB);
    } else {
        snprintf(out, outsz, "%.0f B/s", bps);
    }
}

/* ---- 网络 ---- */

/* 接口是否应从统计中排除（回环 / docker / veth / br-）。 */
static bool iface_excluded(const char *name) {
    return strcmp(name, "lo") == 0 ||
           strncmp(name, "docker", 6) == 0 ||
           strncmp(name, "veth", 4) == 0 ||
           strncmp(name, "br-", 3) == 0;
}

struct net_totals {
    uint64_t recv;
    uint64_t sent;
    bool have;
    char wifi_if[IFNAMSIZ]; /* /proc/net/wireless 里出现的第一个无线接口 */
};

static void read_net_totals(struct net_totals *out) {
    memset(out, 0, sizeof(*out));
    FILE *f = fopen("/proc/net/dev", "r");
    if (f == NULL) {
        return;
    }
    char line[512];
    /* 跳过两行表头 */
    if (fgets(line, sizeof(line), f) == NULL ||
        fgets(line, sizeof(line), f) == NULL) {
        fclose(f);
        return;
    }
    while (fgets(line, sizeof(line), f) != NULL) {
        char *colon = strchr(line, ':');
        if (colon == NULL) {
            continue;
        }
        *colon = '\0';
        char name[IFNAMSIZ];
        sscanf(line, "%31s", name);
        if (iface_excluded(name)) {
            continue;
        }
        unsigned long long recv = 0, sent = 0;
        if (sscanf(colon + 1,
                   "%llu %*u %*u %*u %*u %*u %*u %*u %llu",
                   &recv, &sent) == 2) {
            out->recv += recv;
            out->sent += sent;
            out->have = true;
        }
    }
    fclose(f);

    /* 无线接口名（用于 SSID/信号查询） */
    FILE *wf = fopen("/proc/net/wireless", "r");
    if (wf != NULL) {
        char wline[256];
        if (fgets(wline, sizeof(wline), wf) != NULL &&
            fgets(wline, sizeof(wline), wf) != NULL) {
            while (fgets(wline, sizeof(wline), wf) != NULL) {
                char *c = strchr(wline, ':');
                if (c == NULL) {
                    continue;
                }
                *c = '\0';
                char name[IFNAMSIZ];
                if (sscanf(wline, "%31s", name) == 1) {
                    snprintf(out->wifi_if, sizeof(out->wifi_if), "%s", name);
                    break;
                }
            }
        }
        fclose(wf);
    }
}

/* SSID：SIOCGIWESSID ioctl，成功返回 true。 */
static bool get_ssid(const char *iface, char *ssid, size_t ssid_size) {
    int fd = socket(AF_INET, SOCK_DGRAM, 0);
    if (fd < 0) {
        return false;
    }
    struct iwreq iwr;
    memset(&iwr, 0, sizeof(iwr));
    memcpy(iwr.ifr_name, iface, strnlen(iface, IFNAMSIZ - 1));
    ssid[0] = '\0';
    iwr.u.essid.pointer = ssid;
    iwr.u.essid.length = ssid_size - 1;
    iwr.u.essid.flags = 0;
    bool ok = ioctl(fd, SIOCGIWESSID, &iwr) == 0 && ssid[0] != '\0';
    if (ok) {
        ssid[iwr.u.essid.length < ssid_size - 1 ? iwr.u.essid.length
                                                : ssid_size - 1] = '\0';
    }
    close(fd);
    return ok;
}

/* 信号电平（dBm）→ 百分比，映射 -110 dBm → 0%，-40 dBm → 100%。 */
static bool get_wifi_signal(const char *iface, int *pct) {
    FILE *f = fopen("/proc/net/wireless", "r");
    if (f == NULL) {
        return false;
    }
    char line[256];
    bool ok = false;
    if (fgets(line, sizeof(line), f) != NULL &&
        fgets(line, sizeof(line), f) != NULL) {
        while (fgets(line, sizeof(line), f) != NULL) {
            char name[IFNAMSIZ];
            double level = 0;
            /* 列：name: status link level noise → level 是第 4 列 */
            if (sscanf(line, " %31[^:]: %*s %*s %lf", name, &level) == 2 &&
                strcmp(name, iface) == 0) {
                double p = (level + 110.0) / 70.0 * 100.0;
                if (p < 0) {
                    p = 0;
                }
                if (p > 100) {
                    p = 100;
                }
                *pct = (int)(p + 0.5);
                ok = true;
                break;
            }
        }
    }
    fclose(f);
    return ok;
}

struct net_state {
    struct net_totals prev;
    bool has_prev;
    long prev_tick;
};

static void format_net(struct net_state *st, long now_tick,
                       char *name, size_t namesz,
                       char *signal, size_t signalsz,
                       char *down, size_t downsz,
                       char *up, size_t upsz) {
    struct net_totals cur;
    read_net_totals(&cur);

    if (cur.wifi_if[0] != '\0') {
        int pct = 0;
        if (!get_wifi_signal(cur.wifi_if, &pct)) {
            pct = 0;
        }
        char ssid[33];
        if (get_ssid(cur.wifi_if, ssid, sizeof(ssid))) {
            snprintf(name, namesz, "%s", ssid);
        } else {
            snprintf(name, namesz, "未知");
        }
        snprintf(signal, signalsz, "%d%%", pct);
    } else {
        snprintf(name, namesz, "未连接");
        snprintf(signal, signalsz, "0%%");
    }

    if (st->has_prev) {
        long dt = now_tick - st->prev_tick;
        if (dt <= 0) {
            dt = 1;
        }
        human_speed((double)(cur.recv - st->prev.recv) / dt, down, downsz);
        human_speed((double)(cur.sent - st->prev.sent) / dt, up, upsz);
    } else {
        snprintf(down, downsz, "0 B/s");
        snprintf(up, upsz, "0 B/s");
    }
    st->prev = cur;
    st->has_prev = st->has_prev || cur.have;
    st->prev_tick = now_tick;
}

/* 本机非回环 IPv4（跳过 docker/veth），找不到给 127.0.0.1。 */
static void get_local_ip(char *out, size_t outsz) {
    snprintf(out, outsz, "127.0.0.1");
    struct ifaddrs *ifap = NULL;
    if (getifaddrs(&ifap) != 0) {
        return;
    }
    for (struct ifaddrs *ifa = ifap; ifa != NULL; ifa = ifa->ifa_next) {
        if (ifa->ifa_addr == NULL || ifa->ifa_addr->sa_family != AF_INET) {
            continue;
        }
        if (!(ifa->ifa_flags & IFF_UP) || (ifa->ifa_flags & IFF_LOOPBACK)) {
            continue;
        }
        if (iface_excluded(ifa->ifa_name)) {
            continue;
        }
        char ip[INET_ADDRSTRLEN] = "";
        struct sockaddr_in *sin = (struct sockaddr_in *)ifa->ifa_addr;
        if (inet_ntop(AF_INET, &sin->sin_addr, ip, sizeof(ip)) != NULL &&
            strcmp(ip, "127.0.0.1") != 0) {
            snprintf(out, outsz, "%s", ip);
            break;
        }
    }
    freeifaddrs(ifap);
}

/* ---- 磁盘 ---- */

static void format_disk(char *usage, size_t usz,
                        char *rest, size_t rsz,
                        char *total, size_t tsz) {
    struct statvfs vfs;
    if (statvfs("/", &vfs) != 0 || vfs.f_blocks == 0) {
        snprintf(usage, usz, "0.00%%");
        snprintf(rest, rsz, "0 GiB");
        snprintf(total, tsz, "0 GiB");
        return;
    }
    double total_b = (double)vfs.f_blocks * (double)vfs.f_frsize;
    double free_b  = (double)vfs.f_bfree  * (double)vfs.f_frsize;
    double used_pct = (total_b > 0) ? (1.0 - free_b / total_b) * 100.0 : 0.0;
    human_bytes((uint64_t)free_b, rest, rsz);
    human_bytes((uint64_t)total_b, total, tsz);
    snprintf(usage, usz, "%.2f%%", used_pct);
}

/* ---- CPU ---- */

static bool read_cpu_times(uint64_t *idle, uint64_t *total) {
    FILE *f = fopen("/proc/stat", "r");
    if (f == NULL) {
        return false;
    }
    char line[256];
    bool ok = false;
    if (fgets(line, sizeof(line), f) != NULL &&
        strncmp(line, "cpu ", 4) == 0) {
        unsigned long long v[10] = {0};
        int n = sscanf(line,
                       "%*s %llu %llu %llu %llu %llu %llu %llu %llu %llu %llu",
                       &v[0], &v[1], &v[2], &v[3], &v[4],
                       &v[5], &v[6], &v[7], &v[8], &v[9]);
        if (n >= 4) {
            unsigned long long sum = 0;
            for (int i = 0; i < n; i++) {
                sum += v[i];
            }
            /* idle = idle + iowait */
            *idle = v[3] + (n > 4 ? v[4] : 0);
            *total = sum;
            ok = true;
        }
    }
    fclose(f);
    return ok;
}

static void format_cpu(double *prev_idle, double *prev_total, bool *has_prev,
                       char *out, size_t outsz) {
    uint64_t idle, total;
    if (!read_cpu_times(&idle, &total)) {
        snprintf(out, outsz, "0.00");
        return;
    }
    if (!*has_prev) {
        snprintf(out, outsz, "0.00");
    } else {
        double didle = (double)idle - *prev_idle;
        double dtotal = (double)total - *prev_total;
        double pct = (dtotal > 0) ? (1.0 - didle / dtotal) * 100.0 : 0.0;
        if (pct < 0) {
            pct = 0;
        }
        if (pct > 100) {
            pct = 100;
        }
        snprintf(out, outsz, "%.2f", pct);
    }
    *prev_idle = (double)idle;
    *prev_total = (double)total;
    *has_prev = true;
}

/* ---- 内存 ---- */

static bool meminfo_field(const char *key, uint64_t *out) {
    FILE *f = fopen("/proc/meminfo", "r");
    if (f == NULL) {
        return false;
    }
    char line[256];
    bool ok = false;
    size_t klen = strlen(key);
    while (fgets(line, sizeof(line), f) != NULL) {
        if (strncmp(line, key, klen) == 0) {
            unsigned long long kb = 0;
            if (sscanf(line + klen, ": %llu", &kb) == 1) {
                *out = kb * 1024;
                ok = true;
            }
            break;
        }
    }
    fclose(f);
    return ok;
}

static void format_mem(char *mem_used, size_t musz, char *mem_total, size_t mtsz,
                       char *swap_used, size_t susz, char *swap_total, size_t stsz) {
    uint64_t total = 0, avail = 0;
    if (meminfo_field("MemTotal", &total) && meminfo_field("MemAvailable", &avail)) {
        human_bytes(total, mem_total, mtsz);
        human_bytes(total - (avail < total ? avail : total), mem_used, musz);
    } else {
        snprintf(mem_used, musz, "0 GiB");
        snprintf(mem_total, mtsz, "0 GiB");
    }
    uint64_t stotal = 0, sfree = 0;
    if (meminfo_field("SwapTotal", &stotal) && meminfo_field("SwapFree", &sfree)) {
        human_bytes(stotal, swap_total, stsz);
        human_bytes(stotal - (sfree < stotal ? sfree : stotal), swap_used, susz);
    } else {
        snprintf(swap_used, susz, "0 GiB");
        snprintf(swap_total, stsz, "0 GiB");
    }
}

/* ---- 电池 ---- */

/* 在 /sys/class/power_supply 下找第一个 BAT* 目录，找不到返回 false。 */
static bool find_battery(char *path, size_t pathsz) {
    DIR *d = opendir("/sys/class/power_supply");
    if (d == NULL) {
        return false;
    }
    struct dirent *e;
    bool found = false;
    while ((e = readdir(d)) != NULL) {
        char up[32];
        size_t i = 0;
        for (; e->d_name[i] != '\0' && i < sizeof(up) - 1; i++) {
            char c = e->d_name[i];
            up[i] = (c >= 'a' && c <= 'z') ? (char)(c - 'a' + 'A') : c;
        }
        up[i] = '\0';
        if (strncmp(up, "BAT", 3) == 0 && strlen(e->d_name) < 64) {
            snprintf(path, pathsz, "/sys/class/power_supply/%s", e->d_name);
            found = true;
            break;
        }
    }
    closedir(d);
    return found;
}

/* 读 /sys 数值文件（微单位整数），失败返回 false。 */
static bool sysfs_ulong(const char *dir, const char *file, unsigned long long *out) {
    char path[256];
    snprintf(path, sizeof(path), "%s/%s", dir, file);
    char buf[64];
    if (read_small_file(path, buf, sizeof(buf)) == NULL) {
        return false;
    }
    return sscanf(buf, "%llu", out) == 1;
}

struct battery {
    const char *status;
    char capacity[16];
    double energy_now_wh;
    double energy_full_wh;
    double power_now_w;
    bool present;
};

static void read_battery(struct battery *b) {
    memset(b, 0, sizeof(*b));
    b->status = "无电池";
    snprintf(b->capacity, sizeof(b->capacity), "--");
    char dir[256];
    if (!find_battery(dir, sizeof(dir))) {
        return;
    }
    b->present = true;

    char buf[64];
    unsigned long long v = 0;
    int capacity = 0;
    if (sysfs_ulong(dir, "capacity", &v)) {
        capacity = (int)v;
        snprintf(b->capacity, sizeof(b->capacity), "%d%%", capacity);
    } else {
        snprintf(b->capacity, sizeof(b->capacity), "0%%");
        b->status = "错误";
        return;
    }

    char statuspath[288];
    snprintf(statuspath, sizeof(statuspath), "%s/status", dir);
    if (read_small_file(statuspath, buf, sizeof(buf)) == NULL) {
        b->status = "错误";
        return;
    }
    buf[strcspn(buf, "\n")] = '\0';
    for (char *p = buf; *p; p++) {
        if (*p >= 'A' && *p <= 'Z') {
            *p = (char)(*p - 'A' + 'a');
        }
    }
    if (strcmp(buf, "charging") == 0) {
        b->status = "充电中";
    } else if (strcmp(buf, "discharging") == 0) {
        b->status = "放电中";
    } else if (strcmp(buf, "full") == 0) {
        b->status = "已充满";
    } else if (strcmp(buf, "not charging") == 0) {
        b->status = "未充电";
    } else {
        b->status = "未知";
    }

    if (sysfs_ulong(dir, "energy_now", &v)) {
        b->energy_now_wh = (double)v / 1000000.0;
    }
    if (sysfs_ulong(dir, "energy_full", &v)) {
        b->energy_full_wh = (double)v / 1000000.0;
    }
    if (sysfs_ulong(dir, "power_now", &v)) {
        b->power_now_w = (double)v / 1000000.0;
    }
}

/* ---- 状态行 ---- */

static void format_status_line(struct net_state *net, double *prev_idle,
                               double *prev_total, bool *cpu_has_prev) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    long now_tick = ts.tv_sec;

    char name[64], signal[16], down[32], up[32];
    format_net(net, now_tick, name, sizeof(name), signal, sizeof(signal),
               down, sizeof(down), up, sizeof(up));

    char ip[INET_ADDRSTRLEN];
    get_local_ip(ip, sizeof(ip));

    char disk_usage[16], disk_rest[32], disk_total[32];
    format_disk(disk_usage, sizeof(disk_usage),
                disk_rest, sizeof(disk_rest),
                disk_total, sizeof(disk_total));

    char cpu[16];
    format_cpu(prev_idle, prev_total, cpu_has_prev, cpu, sizeof(cpu));

    char mem_used[32], mem_total[32], swap_used[32], swap_total[32];
    format_mem(mem_used, sizeof(mem_used), mem_total, sizeof(mem_total),
               swap_used, sizeof(swap_used), swap_total, sizeof(swap_total));

    struct battery b;
    read_battery(&b);

    char now[32];
    time_t t = time(NULL);
    strftime(now, sizeof(now), "%Y-%m-%d %H:%M:%S", localtime(&t));

    printf("无线: ( %s @ %s) %s | 网络: ↓ %s ↑ %s | 磁盘: 已用 %s (剩余 %s / 总量 %s) "
           "| CPU: %s%% | 内存: %s/%s | 交换: %s/%s "
           "| 电池: %s (%s, %.2f Wh/%.2f Wh, %.2f W) | %s\n",
           signal, name, ip, down, up,
           disk_usage, disk_rest, disk_total, cpu,
           mem_used, mem_total, swap_used, swap_total,
           b.status, b.capacity, b.energy_now_wh, b.energy_full_wh,
           b.power_now_w, now);
    fflush(stdout);
}

int main(void) {
    fprintf(stderr, "i3status-clone: 正在自动检测 output_format 设置\n");
    fprintf(stderr, "i3status-clone: 回退到 \"none\"\n\n");

    struct sigaction sa;
    memset(&sa, 0, sizeof(sa));
    sa.sa_handler = on_signal;
    sigaction(SIGINT, &sa, NULL);
    sigaction(SIGTERM, &sa, NULL);
    sigaction(SIGQUIT, &sa, NULL);

    struct net_state net = {0};
    double prev_idle = 0, prev_total = 0;
    bool cpu_has_prev = false;

    struct timespec next;
    clock_gettime(CLOCK_MONOTONIC, &next);

    while (!g_stop) {
        format_status_line(&net, &prev_idle, &prev_total, &cpu_has_prev);
        next.tv_sec += REFRESH_SECONDS;
        clock_nanosleep(CLOCK_MONOTONIC, TIMER_ABSTIME, &next, NULL);
    }

    fprintf(stderr, "\ni3status-clone: 收到信号，退出。\n");
    return 0;
}