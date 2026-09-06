################################################################################
#
# machined
#
################################################################################

MACHINED_VERSION = 1.0
MACHINED_SITE = $(BR2_EXTERNAL_TAILSCALEOS_PATH)/internal/machined
MACHINED_SITE_METHOD = local

MACHINED_LICENSE = Proprietary
# Add a LICENSE file at the machined source root and uncomment once you've
# decided on one - Buildroot's license-check tooling expects this for any
# package with a non-trivial license.
# MACHINED_LICENSE_FILES = LICENSE

# The golang-package infrastructure builds with CGO disabled and sets
# GOOS/GOARCH/GOARM automatically from the current Buildroot target
# definition (BR2_ARCH, BR2_ARM_CPU_*, etc)
MACHINED_GOMOD = machined

define MACHINED_INSTALL_TARGET_CMDS
	$(INSTALL) -D -m 0755 $(@D)/bin/machined $(TARGET_DIR)/sbin/init
endef

$(eval $(golang-package))