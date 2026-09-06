# Top-level wrapper Makefile for tailscale-os
#
# Usage:
#   make config BOARD=orangepizero      # apply defconfig
#   make menuconfig BOARD=orangepizero  # interactive config
#   make linux-menuconfig BOARD=orangepizero
#   make uboot-menuconfig BOARD=orangepizero
#   make savedefconfig BOARD=orangepizero
#   make build BOARD=orangepizero       # full build
#   make clean BOARD=orangepizero
#   make all BOARD=orangepizero         # config + build
#   make list                           # show valid BOARD values
#
# BOARD must match a file at configs/<BOARD>_tailscaleos_defconfig

ROOT_DIR   := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BR_DIR     := $(ROOT_DIR)/buildroot
EXT_DIR    := $(ROOT_DIR)
CONFIG_DIR := $(ROOT_DIR)/configs

BOARDS := $(patsubst $(CONFIG_DIR)/%_tailscaleos_defconfig,%,$(wildcard $(CONFIG_DIR)/*_tailscaleos_defconfig))

ifdef BOARD
OUT_DIR    	:= $(ROOT_DIR)/output/$(BOARD)
DEFCONFIG  	:= $(BOARD)_tailscaleos_defconfig
MAKE_BR    	:= $(MAKE) -C $(BR_DIR) O=$(OUT_DIR) BR2_EXTERNAL=$(EXT_DIR)
IMAGES_DIR 	:= $(OUT_DIR)/images
QEMU_ROOTFS	:= $(IMAGES_DIR)/rootfs-qemu.ext2
endif

# Per-board QEMU settings — add an entry here for each board you want to run in QEMU
QEMU_MACHINE.orangepi_zero := orangepi-pc
QEMU_DTB.orangepi_zero     := sun8i-h2-plus-orangepi-zero.dtb
QEMU_ROOTFS_SIZE          := 64M

QEMU_MACHINE := $(QEMU_MACHINE.$(BOARD))
QEMU_DTB     := $(QEMU_DTB.$(BOARD))

.PHONY: all config menuconfig linux-menuconfig uboot-menuconfig \
        savedefconfig build clean distclean list help check-board \
		qemu-rootfs qemu-run qemu-clean check-qemu-board

help:
	@echo "Targets: config menuconfig linux-menuconfig uboot-menuconfig savedefconfig build clean distclean all list"
	@echo "Run with BOARD=<name>, e.g. make build BOARD=orangepizero"
	@echo ""
	@$(MAKE) --no-print-directory list

list:
	@echo "Available boards (from configs/):"
	@for b in $(BOARDS); do echo "  - $$b"; done

check-board:
ifndef BOARD
	$(error BOARD is not set. Usage: make <target> BOARD=<name>. Run 'make list' to see options)
endif
	@test -f "$(CONFIG_DIR)/$(DEFCONFIG)" || \
		(echo "No such defconfig: $(CONFIG_DIR)/$(DEFCONFIG)" && exit 1)

config: check-board
	$(MAKE_BR) $(DEFCONFIG)

menuconfig: check-board
	$(MAKE_BR) menuconfig

linux-menuconfig: check-board
	$(MAKE_BR) linux-menuconfig

uboot-menuconfig: check-board
	$(MAKE_BR) uboot-menuconfig

savedefconfig: check-board
	$(MAKE_BR) BR2_DEFCONFIG=$(CONFIG_DIR)/$(DEFCONFIG) savedefconfig

build: check-board
	$(MAKE_BR)

clean: check-board
	$(MAKE_BR) clean

distclean: check-board
	rm -rf $(OUT_DIR)

check-qemu-board: check-board
	@test -n "$(QEMU_MACHINE)" || \
		(echo "No QEMU_MACHINE defined for BOARD=$(BOARD). Add an entry near the top of the Makefile." && exit 1)

qemu-run: check-qemu-board
	@test -f "$(IMAGES_DIR)/rootfs.ext2" || \
		(echo "rootfs.ext2 not found in $(IMAGES_DIR). Run 'make build BOARD=$(BOARD)' first." && exit 1)
	qemu-system-arm -M $(QEMU_MACHINE) -nic user -nographic \
		-kernel $(IMAGES_DIR)/zImage \
		-dtb $(IMAGES_DIR)/$(QEMU_DTB) \
		-drive file=$(IMAGES_DIR)/rootfs.ext2,if=sd,format=raw \
		-append 'console=ttyS0,115200 root=/dev/mmcblk0 rootwait'

all: config build

# --- machined package convenience targets ----------------------------- 
machined: check-board
	$(MAKE_BR) machined
 
# Re-runs build+install. NOTE: since machined uses SITE_METHOD=local,
# this does NOT re-copy changed source - use machined-reset for that.
machined-rebuild: check-board
	$(MAKE_BR) machined-rebuild
 
# Forces a fresh copy of the local source tree, then rebuilds. This is
# almost always the target you actually want after editing Go source.
machined-reset: check-board
	$(MAKE_BR) machined-dirclean
	$(MAKE_BR) machined

machined-info: check-board
	$(MAKE_BR) machined-show-info
 
br: check-board
	$(MAKE_BR) $(ARGS)