# Serpentine iOS — build, test and TestFlight from the dev Mac. No cloud CI.
# Adapted from phubbard/mapbook (apple-deployment-playbook.md): iOS only, no Catalyst, no .dmg.
#
#   make project            xcodegen → Serpentine.xcodeproj (gitignored)
#   make build              simulator debug build
#   make test               unit tests on a simulator
#   make run                build, install and launch on the simulator
#   make upload-testflight  archive → export .ipa → altool upload
#   make testflight-notes   CHANGELOG [VERSION] → TestFlight What-to-Test + beta-group distribution
#
# One-time: brew install xcodegen; cp Serpentine.local.xcconfig.sample Serpentine.local.xcconfig

SHELL         := /bin/bash
APP_NAME       = Serpentine
APP_BUNDLE_ID  = net.phfactor.serpentine
SCHEME         = Serpentine
TEAM_ID        = NSR65JVW9F
# Simulator names repeat across runtimes, so pin the OS too. `make test SIM_DEVICE="iPhone 16"
# SIM_OS=18.4` checks the deployment floor.
SIM_DEVICE    ?= iPhone 17
SIM_OS        ?= 27.0

BUILD_DIR      = build
DERIVED        = $(BUILD_DIR)/DerivedData
IOS_ARCHIVE    = $(BUILD_DIR)/$(APP_NAME)-iOS.xcarchive
IOS_EXPORT_DIR = $(BUILD_DIR)/testflight-export
TF_EXPORT_OPTS = tools/ExportOptions-TestFlight.plist

VERSION       := $(shell awk '/MARKETING_VERSION:/ {gsub(/[" ]/, "", $$2); print $$2; exit}' project.yml)
BUILD_NUMBER  := $(shell git rev-list --count HEAD 2>/dev/null || echo 0)
GIT_SHA       := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

LOCAL = $(APP_NAME).local.xcconfig
APP_STORE_KEY_ID    ?= $(shell awk -F'=' '/^APP_STORE_KEY_ID/ {gsub(/[ ;]/,"",$$2); print $$2; exit}' $(LOCAL) 2>/dev/null)
APP_STORE_ISSUER_ID ?= $(shell awk -F'=' '/^APP_STORE_ISSUER_ID/ {gsub(/[ ;]/,"",$$2); print $$2; exit}' $(LOCAL) 2>/dev/null)
TESTFLIGHT_GROUPS   ?= $(shell awk -F'=' '/^TESTFLIGHT_GROUPS/ {sub(/^[^=]*=/,""); gsub(/^[ \t]+|[ \t;]+$$/,""); print; exit}' $(LOCAL) 2>/dev/null)

SIM = -destination 'platform=iOS Simulator,name=$(SIM_DEVICE),OS=$(SIM_OS)'
SIM_UDID = $(shell xcrun simctl list devices available -j | ruby -rjson -e 'd=JSON.parse(STDIN.read)["devices"]; k=d.keys.find{|r| r.end_with?("iOS-"+ARGV[1].tr(".","-"))}; puts((d[k]||[]).find{|x| x["name"]==ARGV[0]}&.fetch("udid"))' "$(SIM_DEVICE)" "$(SIM_OS)")
XCB = xcodebuild -project $(APP_NAME).xcodeproj -scheme $(SCHEME) -derivedDataPath $(DERIVED)

.PHONY: help version project build test run verify-testflight archive-testflight upload-testflight \
        testflight-notes testflight-groups icon clean

help:
	@echo "Serpentine $(VERSION)+$(BUILD_NUMBER) ($(GIT_SHA))"
	@sed -n 's/^#   make /  make /p' Makefile

version:
	@echo "$(VERSION)+$(BUILD_NUMBER) ($(GIT_SHA))"

project:
	@xcodegen generate --quiet

build: project
	@$(XCB) $(SIM) -configuration Debug build | grep -E "^(error|warning):|\*\* BUILD" || true
	@test -d "$(DERIVED)/Build/Products/Debug-iphonesimulator/$(APP_NAME).app" || { echo "error: build failed"; exit 1; }

test: project
	@mkdir -p $(BUILD_DIR)
	@set -o pipefail; $(XCB) $(SIM) test 2>&1 | tee $(BUILD_DIR)/test.log | grep -E "error:|✔|✘|Test run|\*\* TEST" \
	    || { echo "error: tests failed (full log: $(BUILD_DIR)/test.log)"; exit 1; }

run: build
	@test -n "$(SIM_UDID)" || { echo "error: no simulator '$(SIM_DEVICE)' on iOS $(SIM_OS)"; exit 1; }
	@xcrun simctl boot $(SIM_UDID) 2>/dev/null || true
	@open -a Simulator 2>/dev/null || true
	@xcrun simctl install $(SIM_UDID) "$(DERIVED)/Build/Products/Debug-iphonesimulator/$(APP_NAME).app"
	@xcrun simctl launch $(SIM_UDID) $(APP_BUNDLE_ID)

icon:
	@swift tools/make_icon.swift Serpentine/Assets.xcassets/AppIcon.appiconset/AppIcon-1024.png

# ─── TestFlight (playbook §4–6) ──────────────────────────────────────────────
verify-testflight:
	@test -n "$(APP_STORE_KEY_ID)" || { echo "error: APP_STORE_KEY_ID not set in $(LOCAL)"; exit 1; }
	@test -n "$(APP_STORE_ISSUER_ID)" || { echo "error: APP_STORE_ISSUER_ID not set in $(LOCAL)"; exit 1; }
	@test -f "$$HOME/.appstoreconnect/private_keys/AuthKey_$(APP_STORE_KEY_ID).p8" || \
	    { echo "error: ~/.appstoreconnect/private_keys/AuthKey_$(APP_STORE_KEY_ID).p8 missing"; exit 1; }
	@echo "✓ ASC key $(APP_STORE_KEY_ID)"

archive-testflight: verify-testflight project
	@rm -rf $(IOS_ARCHIVE) $(IOS_EXPORT_DIR)
	@xcodebuild -project $(APP_NAME).xcodeproj -scheme $(SCHEME) -configuration Release \
	    -destination 'generic/platform=iOS' -archivePath $(IOS_ARCHIVE) \
	    -allowProvisioningUpdates archive | grep -E "^(error|warning):|\*\* ARCHIVE" || true
	@test -d $(IOS_ARCHIVE) || { echo "error: archive failed"; exit 1; }
	@echo "✓ archived $(VERSION)+$(BUILD_NUMBER)"

upload-testflight: archive-testflight
	@xcodebuild -exportArchive -archivePath $(IOS_ARCHIVE) -exportPath $(IOS_EXPORT_DIR) \
	    -exportOptionsPlist $(TF_EXPORT_OPTS) -allowProvisioningUpdates | grep -E "^(error|warning):|EXPORT" || true
	@test -f $(IOS_EXPORT_DIR)/$(APP_NAME).ipa || { echo "error: no .ipa exported"; exit 1; }
	@xcrun altool --upload-app --type ios --file $(IOS_EXPORT_DIR)/$(APP_NAME).ipa \
	    --apiKey $(APP_STORE_KEY_ID) --apiIssuer $(APP_STORE_ISSUER_ID)
	@echo "✓ uploaded; TestFlight processing takes 10-30 min. Then: make testflight-notes"

# iOS only, so one build per version (mapbook uses 2 for iOS + Catalyst).
MIN_BUILDS ?= 1
testflight-notes: verify-testflight
	@ruby tools/set-testflight-notes.rb --key-id "$(APP_STORE_KEY_ID)" --issuer "$(APP_STORE_ISSUER_ID)" \
	    --bundle-id "$(APP_BUNDLE_ID)" --version "$(VERSION)" --build "$(BUILD_NUMBER)" \
	    --min-builds "$(MIN_BUILDS)" $(if $(strip $(TESTFLIGHT_GROUPS)),--groups "$(TESTFLIGHT_GROUPS)",) \
	    --changelog CHANGELOG.md

testflight-groups:
	@ruby tools/set-testflight-notes.rb --key-id "$(APP_STORE_KEY_ID)" --issuer "$(APP_STORE_ISSUER_ID)" \
	    --bundle-id "$(APP_BUNDLE_ID)" --list-groups

clean:
	@rm -rf $(BUILD_DIR)
