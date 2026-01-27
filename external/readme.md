# NOTICE

- use my `i3-wm_4.24-1-modified_amd64.deb` provide here

```bash
sudo dpkg -i i3-wm_4.24-1-modified_amd64.deb
```

# unsafe script here

1. damn, why i3wm i3 stuff depends on i3status
2. can I have private repo for building up i3 i3wm, which mainly just reference to git repo

# todo

- [ ] I need to learn something from apt or dpkg so far

# solution?

1. download i3wm and i3 deb package

```bash
apt download i3wm
apt download i3
```

### 需求确认

你已经通过 `apt download` 获取了 `i3_4.24-1_amd64.deb` 和 `i3-wm_4.24-1_amd64.deb` 两个二进制包，核心需求是**直接修改这两个 .deb 包（去掉对 i3status 的依赖）**，无需搭建私有 APT 仓库，修改后直接本地安装。

这是完全可行的！.deb 本质是归档文件，我们可以解压 → 修改依赖配置 → 重新打包，全程在本地完成，无需编译源码/搭建仓库。

---

## 完整步骤：本地修改 .deb 包（去掉 i3status 依赖）

### 前置知识

.deb 包的结构（解压后可见）：

- `debian-binary`：标记 deb 格式版本（如 `2.0`）；
- `control.tar.xz`：核心配置（包含依赖、安装脚本等，我们要改的 `control` 文件在这里）；
- `data.tar.xz`：程序实际文件（无需修改）。

### 步骤 1：创建临时工作目录（避免文件混乱）

```bash
# 创建总工作目录
mkdir -p ~/i3-deb-modify && cd ~/i3-deb-modify

# 复制下载的 deb 包到工作目录（替换为你的实际路径）
cp ~/下载/i3_4.24-1_amd64.deb .
cp ~/下载/i3-wm_4.24-1_amd64.deb .

# 为每个包创建独立解压目录
mkdir -p i3-deb i3-wm-deb
```

### 步骤 2：先修改 `i3-wm_4.24-1_amd64.deb`（核心依赖包）

#### 2.1 解压 i3-wm deb 包

```bash
cd ~/i3-deb-modify/i3-wm-deb

# 解压 deb 包（-x：解压，-f：指定文件，-v：显示过程）
ar x ../i3-wm_4.24-1_amd64.deb

# 解压 control.tar.xz（依赖配置在里面）
tar -xf control.tar.xz
```

#### 2.2 修改依赖配置（删除 i3status 相关）

```bash
# 编辑 control 文件（核心：去掉 Depends/Recommends 中的 i3status）
nano control
```

找到类似以下的行（不同版本格式略有差异）：

```ini
Package: i3-wm
Version: 4.24-1
Architecture: amd64
Maintainer: Michael Stapelberg <stapelberg@debian.org>
Installed-Size: 2932
Depends: libc6 (>= 2.38), libcairo2 (>= 1.14.0), libev4 (>= 1:4.04), libglib2.0-0 (>= 2.37.3), libpango-1.0-0 (>= 1.14.0), libpangocairo-1.0-0 (>= 1.14.0), libstartup-notification0 (>= 0.7), libxcb-cursor0 (>= 0.1.1), libxcb-icccm4 (>= 0.4.1), libxcb-keysyms1 (>= 0.4.0), libxcb-randr0 (>= 1.12), libxcb-shape0 (>= 1.12), libxcb-xinerama0 (>= 1.12), libxcb-xkb1 (>= 1.12), libxcb-xrm0 (>= 0.0.0), libxcb1 (>= 1.12), libyajl2 (>= 2.0.4), x11-utils, i3status (>= 2.14-1)  # 这里有 i3status 依赖
Recommends: suckless-tools, dmenu, i3status, i3lock, xfonts-terminus
```

**修改规则**：

- 删除 `Depends` 行中的 `i3status (>= 2.14-1)`；
- 删除 `Recommends` 行中的 `i3status`；

修改后示例：

```ini
Package: i3-wm
Version: 4.24-1
Architecture: amd64
Maintainer: Michael Stapelberg <stapelberg@debian.org>
Installed-Size: 2932
Depends: libc6 (>= 2.38), libcairo2 (>= 1.14.0), libev4 (>= 1:4.04), libglib2.0-0 (>= 2.37.3), libpango-1.0-0 (>= 1.14.0), libpangocairo-1.0-0 (>= 1.14.0), libstartup-notification0 (>= 0.7), libxcb-cursor0 (>= 0.1.1), libxcb-icccm4 (>= 0.4.1), libxcb-keysyms1 (>= 0.4.0), libxcb-randr0 (>= 1.12), libxcb-shape0 (>= 1.12), libxcb-xinerama0 (>= 1.12), libxcb-xkb1 (>= 1.12), libxcb-xrm0 (>= 0.0.0), libxcb1 (>= 1.12), libyajl2 (>= 2.0.4), x11-utils
Recommends: suckless-tools, dmenu, i3lock, xfonts-terminus
```

保存退出（nano 按 `Ctrl+O` → 回车 → `Ctrl+X`）。

#### 2.3 重新打包 control.tar.xz

```bash
# 删除原 control.tar.xz，重新打包（保留权限，否则 dpkg 安装报错）
rm control.tar.xz
tar -cJf control.tar.xz ./control ./md5sums ./postinst ./prerm ./postrm  # 保留原有脚本文件（./postinst 等）
```

#### 2.4 重新打包完整的 .deb 包

```bash
# 回到 i3-wm-deb 目录，重新打包 deb（顺序必须是：debian-binary → control.tar.xz → data.tar.xz）
ar rcs ../i3-wm_4.24-1-modified_amd64.deb debian-binary control.tar.xz data.tar.xz

# 验证打包结果（能看到文件即为成功）
ls ../i3-wm_4.24-1-modified_amd64.deb
```

### 步骤 3：修改 `i3_4.24-1_amd64.deb`（元包，依赖 i3-wm）

`i3.deb` 是“元包”（仅依赖 i3-wm 等组件，无实际文件），需同步去掉 i3status 依赖：

```bash
cd ~/i3-deb-modify/i3-deb

# 解压 deb 包
ar x ../i3_4.24-1_amd64.deb

# 解压 control.tar.xz
tar -xf control.tar.xz

# 编辑 control 文件，删除 i3status 依赖
nano control
```

修改前示例：

```ini
Package: i3
Version: 4.24-1
Architecture: amd64
Maintainer: Michael Stapelberg <stapelberg@debian.org>
Installed-Size: 24
Depends: i3-wm (= 4.24-1), i3status (>= 2.14-1), i3lock, suckless-tools
Recommends: dmenu, i3blocks, xfonts-terminus
```

修改后示例：

```ini
Package: i3
Version: 4.24-1
Architecture: amd64
Maintainer: Michael Stapelberg <stapelberg@debian.org>
Installed-Size: 24
Depends: i3-wm (= 4.24-1), i3lock, suckless-tools
Recommends: dmenu, i3blocks, xfonts-terminus
```

#### 3.1 重新打包 control.tar.xz 和 deb 包

```bash
rm control.tar.xz
tar -cJf control.tar.xz ./control ./md5sums

# 重新打包 deb
ar rcs ../i3_4.24-1-modified_amd64.deb debian-binary control.tar.xz data.tar.xz

# 回到总目录，查看修改后的包
cd ~/i3-deb-modify
ls -l *modified*.deb
# 应看到：
# i3_4.24-1-modified_amd64.deb
# i3-wm_4.24-1-modified_amd64.deb
```

### 步骤 4：安装修改后的 .deb 包

```bash
cd ~/i3-deb-modify

# 先卸载原有 i3/i3-wm（如果已安装）
sudo apt remove -y i3 i3-wm

# 安装修改后的包（先装 i3-wm，再装 i3 元包）
sudo dpkg -i i3-wm_4.24-1-modified_amd64.deb
sudo dpkg -i i3_4.24-1-modified_amd64.deb

# 修复可能的依赖缺失（dpkg 不会自动解决依赖，用 apt 补）
sudo apt -f install
```

### 步骤 5：验证依赖是否修改成功

```bash
# 查看 i3-wm 的依赖，已无 i3status
apt-cache depends i3-wm | grep -i i3status
# 无输出 = 依赖已移除！

# 查看 i3 元包的依赖
apt-cache depends i3 | grep -i i3status
# 无输出 = 成功！
```

---

## 关键注意事项（避坑）

### 1. 打包顺序必须正确

重新打包 deb 时，`ar` 命令的文件顺序必须是：`debian-binary` → `control.tar.xz` → `data.tar.xz`，否则 dpkg 会报错 `invalid debian package format`。

### 2. 保留 control.tar.xz 中的所有文件

解压 `control.tar.xz` 后，除了 `control`，还有 `md5sums`、`postinst`（安装后脚本）、`prerm`（卸载前脚本）等，重新打包时要包含这些文件（否则安装/卸载可能异常）。

### 3. 权限问题

修改文件时确保权限不变（解压后默认权限正确，不要手动改 chmod），否则 dpkg 安装会提示“权限错误”。

### 4. 版本号标记（可选）

我们在包名后加了 `modified` 标记（如 `i3-wm_4.24-1-modified`），目的是区分官方包，避免 `apt upgrade` 覆盖（如果想禁止官方升级，可执行：`sudo apt-mark hold i3 i3-wm`）。

---

## 总结

1. **核心逻辑**：.deb 是归档文件，无需编译源码，直接解压 → 修改 `control` 文件（删除 i3status 依赖）→ 重新打包即可；
2. **关键步骤**：
   - 解压 deb：`ar x <包名>.deb`；
   - 解压控制文件：`tar -xf control.tar.xz`；
   - 修改 `control` 中的 `Depends`/`Recommends`；
   - 重新打包：`ar rcs <新包名>.deb debian-binary control.tar.xz data.tar.xz`；
3. **优势**：无需搭建仓库、无需编译源码，纯本地修改，快速解决依赖问题；
4. **兜底**：若修改出错，可通过 `sudo apt install --reinstall i3 i3-wm` 恢复官方包。

这样你就彻底摆脱了 i3wm 对 i3status 的依赖，且全程在本地完成，无需复杂的仓库配置！

You want to add a specific package to APT's ignore list so that it won't be automatically upgraded when you run system update commands like `apt upgrade`. Here are two reliable methods to achieve this, with step-by-step instructions:

---

## Method 1: Use `apt-mark hold` (Simplest & Most Common)

This is the easiest way to lock a package from upgrades, and it’s the recommended approach for most use cases.

### Step 1: Basic commands

1. **Lock the package** (replace `<package-name>` with the actual package name, e.g., `nginx`):

   ```bash
   sudo apt-mark hold <package-name>
   ```

   Example (lock `nginx`):

   ```bash
   sudo apt-mark hold nginx
   ```

   Output (success): `nginx set on hold.`

2. **Check which packages are locked** (verify the package is in the hold list):

   ```bash
   apt-mark showhold
   ```

   Output (if `nginx` is locked): `nginx`

3. **Unlock the package** (if you want to allow upgrades later):
   ```bash
   sudo apt-mark unhold <package-name>
   ```
   Example:
   ```bash
   sudo apt-mark unhold nginx
   ```
   Output: `nginx cancelled hold.`

## Method 2: Use `/etc/apt/preferences` (More Flexible)

This method uses APT’s preference system to set a low priority for the package, preventing upgrades. It’s useful if you need advanced rules (e.g., lock multiple packages or specific versions).

### Step 1: Create/Edit the preferences file

1. Open the file with a text editor (e.g., nano):

   ```bash
   sudo nano /etc/apt/preferences
   ```

2. Add the following content (replace `<package-name>` with your package):

   ```
   # Lock <package-name> from upgrades
   Package: <package-name>
   Pin: release *
   Pin-Priority: -1
   ```

   - `Package`: Specifies the target package (use `*` to lock all packages, but this is not recommended).
   - `Pin: release *`: Matches all repository releases.
   - `Pin-Priority: -1`: Tells APT to never upgrade this package (priority < 0 means "reject").

3. Save and exit (for nano: press `Ctrl+O`, then `Enter`, then `Ctrl+X`).

### Step 2: Verify the configuration

Run a **simulated upgrade** to confirm the package won’t be upgraded:

```bash
sudo apt update && sudo apt upgrade -s
```

Look for lines like:

```
The following packages have been kept back:
  <package-name>
```

This confirms the package is ignored during upgrades.

---

### How to Verify the Lock Works

Run the simulated upgrade command above (`apt upgrade -s`). If the package is listed under "kept back", the lock is effective.

---

### Summary

1. **Simplest method**: Use `sudo apt-mark hold <package-name>` to lock a package, and `sudo apt-mark unhold <package-name>` to unlock it.
2. **Advanced method**: Edit `/etc/apt/preferences` and set `Pin-Priority: -1` for the target package to block upgrades.
3. **Verification**: Always test with `apt upgrade -s` (simulated upgrade) to confirm the package is "kept back".
