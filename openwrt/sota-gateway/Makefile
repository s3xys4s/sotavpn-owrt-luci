include $(TOPDIR)/rules.mk
PKG_NAME:=sota-gateway
PKG_VERSION:=0.1.0
PKG_RELEASE:=19
include $(INCLUDE_DIR)/package.mk
RSTRIP:=:

define Package/sota-gateway
  SECTION:=net
  CATEGORY:=Network
  TITLE:=Sota VPN gateway
  DEPENDS:=@aarch64 +kmod-tun +ip-full +firewall4 +ca-bundle +dnsmasq +smartdns
endef

define Package/sota-gateway/conffiles
/etc/config/sota
/etc/sota/
endef

define Build/Compile
endef

define Package/sota-gateway/install
	$(INSTALL_DIR) $(1)
	$(CP) ./files/. $(1)/
	find $(1) -type d -exec chmod 0755 {} +
	find $(1) -type f -exec chmod 0644 {} +
	chmod 0755 $(1)/etc/init.d/sota $(1)/usr/libexec/sota-policy $(1)/usr/libexec/rpcd/sota
	$(INSTALL_DIR) $(1)/usr/libexec/sota
	$(INSTALL_BIN) ./artifacts/kittend $(1)/usr/libexec/sota/kittend
	$(INSTALL_BIN) ./artifacts/panel $(1)/usr/libexec/sota/controller
endef
$(eval $(call BuildPackage,sota-gateway))
