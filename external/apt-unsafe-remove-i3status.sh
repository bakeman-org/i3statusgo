#!/bin/bash
set -euo pipefail

# 颜色输出函数
red() { echo -e "\033[31m$1\033[0m"; }
green() { echo -e "\033[32m$1\033[0m"; }
yellow() { echo -e "\033[33m$1\033[0m"; }
blue() { echo -e "\033[34m$1\033[0m"; }

# 步骤1：检查输入参数（必须指定包名）
if [ $# -ne 1 ]; then
	red "用法错误！正确用法：$0 <包名>（例如：$0 i3status）"
	exit 1
fi
PACKAGE="$1"

# 步骤2：检查必要命令
required_cmds=("apt-file" "rm" "awk" "sort" "uniq")
for cmd in "${required_cmds[@]}"; do
	if ! command -v "$cmd" &>/dev/null; then
		yellow "缺少命令 $cmd，正在自动安装..."
		sudo apt update && sudo apt install -y "$cmd" || {
			red "安装 $cmd 失败，请手动安装后重试！"
			exit 1
		}
	fi
done

# 步骤3：高风险提示（必须确认）
red "==================== 高风险警告 ===================="
red "1. 该脚本会手动删除 $PACKAGE 包的文件（绕过 apt 管理）；"
red "2. 可能导致 dpkg 数据库与实际文件不一致，后续 apt 操作可能出错；"
red "3. 仅建议在确认无依赖风险时使用（如自定义编译的包）；"
red "===================================================="
read -p "确认继续？（输入 YES 表示同意，否则退出）：" CONFIRM
if [ "$CONFIRM" != "YES" ]; then
	green "操作已取消！"
	exit 0
fi

# 步骤4：解析 apt-file list 输出，提取纯文件路径
blue "\n===== 提取 $PACKAGE 的文件列表 ====="
# 解析规则：去掉前缀（如 i3status:），只保留路径，去重，过滤空行
FILES=$(apt-file list "$PACKAGE" | awk -F': ' '{print $2}' | sort -u | grep -v '^$')

if [ -z "$FILES" ]; then
	yellow "未找到 $PACKAGE 对应的文件列表！"
	exit 0
fi

# 显示要删除的文件清单
echo "即将处理的文件列表（共 $(echo "$FILES" | wc -l) 个）："
echo "$FILES"
read -p "确认要删除以上文件？[y/N] " DELETE_CONFIRM
if [[ ! "$DELETE_CONFIRM" =~ ^[Yy]$ ]]; then
	green "操作已取消！"
	exit 0
fi

# 步骤5：交互式删除每个文件（rm -i）
blue "\n===== 开始交互式删除（按 y 确认删除，n 跳过） ====="
for FILE in $FILES; do
	# 先检查文件是否存在，避免删空
	if [ -e "$FILE" ]; then
		echo -n "删除 $FILE？[y/n] "
		read -n1 ANSWER
		echo "" # 换行
		if [ "$ANSWER" = "y" ] || [ "$ANSWER" = "Y" ]; then
			sudo rm "$FILE" # 双重确认（rm -i 会再问一次）
			if [ $? -eq 0 ]; then
				green "✅ 已删除：$FILE"
			else
				red "❌ 删除失败：$FILE"
			fi
		else
			yellow "⏭️  跳过：$FILE"
		fi
	else
		yellow "⚠️  文件不存在，跳过：$FILE"
	fi
done

# 步骤6：完成提示
green "\n===== 删除流程完成 ====="
yellow "提示：若需恢复 dpkg 数据库，可执行 sudo apt install --reinstall $PACKAGE"
