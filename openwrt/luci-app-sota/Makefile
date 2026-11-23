include $(TOPDIR)/rules.mk
PKG_NAME:=luci-app-sota
PKG_VERSION:=0.1.0
PKG_RELEASE:=15
PKG_BUILD_DEPENDS:=luci-base/host
include $(INCLUDE_DIR)/package.mk
define Package/luci-app-sota
  SECTION:=luci
  CATEGORY:=LuCI
  TITLE:=Sota Gateway controls
  DEPENDS:=+luci-base +sota-gateway
  PKGARCH:=all
endef
define Build/Compile
	$(STAGING_DIR_HOST)/bin/po2lmo ./po/ru/sota.po $(PKG_BUILD_DIR)/sota.ru.lmo
endef
define Package/luci-app-sota/install
	$(INSTALL_DIR) $(1)
	$(CP) ./files/. $(1)/
	$(INSTALL_DATA) ./routing.js $(1)/www/luci-static/resources/sota-routing.js
	$(INSTALL_DATA) ./i18n.js ./messages.json $(1)/www/luci-static/resources/sota/
	$(INSTALL_DIR) $(1)/usr/lib/lua/luci/i18n
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/sota.ru.lmo $(1)/usr/lib/lua/luci/i18n/sota.ru.lmo
	find $(1) -type d -exec chmod 0755 {} +
	find $(1) -type f -exec chmod 0644 {} +
endef
$(eval $(call BuildPackage,luci-app-sota))
