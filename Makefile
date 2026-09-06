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
OUT_DIR    := $(ROOT_DIR)/output/$(BOARD)
DEFCONFIG  := $(BOARD)_tailscaleos_defconfig
MAKE_BR    := $(MAKE) -C $(BR_DIR) O=$(OUT_DIR) BR2_EXTERNAL=$(EXT_DIR)
endif

.PHONY: all config menuconfig linux-menuconfig uboot-menuconfig \
        savedefconfig build clean distclean list help check-board

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

all: config build