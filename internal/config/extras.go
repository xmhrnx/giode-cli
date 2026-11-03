package config

import (
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
)

// androidPermissions maps short permission names to manifest names.
var androidPermissions = map[string][]string{
	// gogio's names, which it also adds for imports of
	// gioui.org/app/permission/...
	"network":      {"android.permission.INTERNET"},
	"networkstate": {"android.permission.ACCESS_NETWORK_STATE"},
	"bluetooth":    {"android.permission.BLUETOOTH", "android.permission.BLUETOOTH_ADMIN", "android.permission.ACCESS_FINE_LOCATION"},
	"camera":       {"android.permission.CAMERA"},
	"storage":      {"android.permission.READ_EXTERNAL_STORAGE", "android.permission.WRITE_EXTERNAL_STORAGE"},
	"wakelock":     {"android.permission.WAKE_LOCK"},
	"microphone":   {"android.permission.RECORD_AUDIO"},
	// giode's.
	"notifications":      {"android.permission.POST_NOTIFICATIONS"},
	"foreground_service": {"android.permission.FOREGROUND_SERVICE", "android.permission.FOREGROUND_SERVICE_DATA_SYNC"},
	"vibrate":            {"android.permission.VIBRATE"},
	"biometric":          {"android.permission.USE_BIOMETRIC"},
	"location":           {"android.permission.ACCESS_FINE_LOCATION", "android.permission.ACCESS_COARSE_LOCATION"},
	"coarse_location":    {"android.permission.ACCESS_COARSE_LOCATION"},
	"contacts":           {"android.permission.READ_CONTACTS"},
	"calendar":           {"android.permission.READ_CALENDAR"},
	"boot":               {"android.permission.RECEIVE_BOOT_COMPLETED"},
}

func androidPermission(p string) ([]string, error) {
	if strings.Contains(p, ".") {
		return []string{p}, nil
	}
	if full, ok := androidPermissions[p]; ok {
		return full, nil
	}
	return nil, fmt.Errorf("unknown Android permission %q; use a full name (android.permission.X) or one of: %s", p, strings.Join(sortedKeys(androidPermissions), ", "))
}

// keepAliveService declares giode's foreground service (see
// giode/internal/mobile/KeepAliveService.java).
const keepAliveService = `		<service android:name="org.giode.mobile.KeepAliveService"
			android:foregroundServiceType="dataSync"
			android:exported="false"/>`

// shareActivity declares giode's share target (see
// giode/internal/mobile/ShareActivity.java): any files and text.
const shareActivity = `		<activity android:name="org.giode.mobile.ShareActivity"
			android:exported="true"
			android:excludeFromRecents="true"
			android:noHistory="true"
			android:taskAffinity=""
			android:theme="@android:style/Theme.Translucent.NoTitleBar">
			<intent-filter>
				<action android:name="android.intent.action.SEND"/>
				<category android:name="android.intent.category.DEFAULT"/>
				<data android:mimeType="*/*"/>
			</intent-filter>
			<intent-filter>
				<action android:name="android.intent.action.SEND_MULTIPLE"/>
				<category android:name="android.intent.category.DEFAULT"/>
				<data android:mimeType="*/*"/>
			</intent-filter>
		</activity>`

// AndroidExtras returns the permissions, <manifest> XML and
// <application> XML giode adds to the Android manifest.
func (c *Config) AndroidExtras() (perms []string, manifest, application string) {
	add := func(names ...string) {
		for _, n := range names {
			full, _ := androidPermission(n)
			perms = append(perms, full...)
		}
	}
	if c.Android.Notifications == nil || *c.Android.Notifications {
		add("notifications")
	}
	if c.Android.KeepAlive {
		add("foreground_service", "notifications")
	}
	add(c.Android.Permissions...)
	perms = dedupe(perms)
	application = strings.TrimSpace(c.Android.Application)
	if c.Android.KeepAlive {
		application = strings.TrimSpace(keepAliveService + "\n" + application)
	}
	if c.App.ShareTarget {
		application = strings.TrimSpace(shareActivity + "\n" + application)
	}
	return perms, strings.TrimSpace(c.Android.Manifest), application
}

// AndroidApplicationAttrs returns android.application_attributes as
// XML attributes, each with a leading space, sorted by name.
func (c *Config) AndroidApplicationAttrs() string {
	var b strings.Builder
	for _, k := range sortedKeys(c.Android.ApplicationAttributes) {
		name := k
		if !strings.Contains(name, ":") {
			name = "android:" + name
		}
		fmt.Fprintf(&b, ` %s="%s"`, name, html.EscapeString(c.Android.ApplicationAttributes[k]))
	}
	return b.String()
}

// usageKeys maps short usage names to Info.plist keys.
var usageKeys = map[string]string{
	"camera":          "NSCameraUsageDescription",
	"microphone":      "NSMicrophoneUsageDescription",
	"speech":          "NSSpeechRecognitionUsageDescription",
	"photos":          "NSPhotoLibraryUsageDescription",
	"photos_add":      "NSPhotoLibraryAddUsageDescription",
	"location":        "NSLocationWhenInUseUsageDescription",
	"location_always": "NSLocationAlwaysAndWhenInUseUsageDescription",
	"contacts":        "NSContactsUsageDescription",
	"calendar":        "NSCalendarsUsageDescription",
	"reminders":       "NSRemindersUsageDescription",
	"bluetooth":       "NSBluetoothAlwaysUsageDescription",
	"faceid":          "NSFaceIDUsageDescription",
	"motion":          "NSMotionUsageDescription",
	"health":          "NSHealthShareUsageDescription",
	"health_update":   "NSHealthUpdateUsageDescription",
	"tracking":        "NSUserTrackingUsageDescription",
	"local_network":   "NSLocalNetworkUsageDescription",
}

// IOSPlist returns the Info.plist entries giode adds for iOS, as plist
// XML key/value pairs.
func (c *Config) IOSPlist() (string, error) {
	raw := c.IOS.Plist
	if c.IOS.Push != "" || c.App.ShareTarget {
		raw = make(map[string]any, len(c.IOS.Plist)+3)
		for k, v := range c.IOS.Plist {
			raw[k] = v
		}
	}
	if c.App.ShareTarget {
		// Where giode looks for what the share extension received.
		raw["GiodeAppGroup"] = c.IOSAppGroup()
		raw["GiodeShareScheme"] = c.IOSShareScheme()
	}
	if c.IOS.Push != "" {
		modes := []string{"remote-notification"}
		if old, ok := raw["UIBackgroundModes"].([]any); ok {
			for _, m := range old {
				if s, ok := m.(string); ok && s != "remote-notification" {
					modes = append(modes, s)
				}
			}
		}
		raw["UIBackgroundModes"] = modes
	}
	return infoPlist(c.IOS.Usage, raw)
}

// IOSAppGroup is the App Group app.share_target's extension shares
// with the app.
func (c *Config) IOSAppGroup() string {
	if c.IOS.AppGroup != "" {
		return c.IOS.AppGroup
	}
	return "group." + c.App.ID
}

// IOSShareScheme is the URL scheme app.share_target's extension opens
// the app with.
func (c *Config) IOSShareScheme() string {
	return "giode-share-" + c.App.ID
}

// MacOSPlist returns the Info.plist entries giode adds for macOS.
func (c *Config) MacOSPlist() (string, error) {
	raw := c.MacOS.Plist
	if len(c.ContextActions) > 0 || len(c.FileTypes) > 0 {
		raw = make(map[string]any, len(c.MacOS.Plist)+3)
		for k, v := range c.MacOS.Plist {
			raw[k] = v
		}
		if _, ok := raw["NSServices"]; !ok && len(c.ContextActions) > 0 {
			raw["NSServices"] = c.macServices()
		}
		for k, v := range c.macFileTypes() {
			if _, ok := raw[k]; !ok {
				raw[k] = v
			}
		}
	}
	return infoPlist(c.MacOS.Usage, raw)
}

func infoPlist(usage map[string]string, raw map[string]any) (string, error) {
	all := make(map[string]any, len(usage)+len(raw))
	for k, v := range usage {
		all[usageKeys[k]] = v
	}
	for k, v := range raw {
		all[k] = v
	}
	return plistXML(all)
}

// plistXML renders a flat map as plist key/value pairs, sorted by key.
func plistXML(m map[string]any) (string, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		val, err := plistValue(m[k])
		if err != nil {
			return "", fmt.Errorf("plist key %s: %w", k, err)
		}
		fmt.Fprintf(&b, "\t<key>%s</key>\n\t%s\n", html.EscapeString(k), val)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func plistValue(v any) (string, error) {
	switch v := v.(type) {
	case string:
		return "<string>" + html.EscapeString(v) + "</string>", nil
	case bool:
		if v {
			return "<true/>", nil
		}
		return "<false/>", nil
	case int64:
		return "<integer>" + strconv.FormatInt(v, 10) + "</integer>", nil
	case int:
		return "<integer>" + strconv.Itoa(v) + "</integer>", nil
	case float64:
		return "<real>" + strconv.FormatFloat(v, 'g', -1, 64) + "</real>", nil
	case []any:
		var b strings.Builder
		b.WriteString("<array>")
		for _, e := range v {
			s, err := plistValue(e)
			if err != nil {
				return "", err
			}
			b.WriteString(s)
		}
		b.WriteString("</array>")
		return b.String(), nil
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("<dict>")
		for _, k := range keys {
			s, err := plistValue(v[k])
			if err != nil {
				return "", err
			}
			b.WriteString("<key>" + html.EscapeString(k) + "</key>" + s)
		}
		b.WriteString("</dict>")
		return b.String(), nil
	case []string:
		a := make([]any, len(v))
		for i, s := range v {
			a[i] = s
		}
		return plistValue(a)
	}
	return "", fmt.Errorf("unsupported value %v (%T); use a string, boolean, number or list", v, v)
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	out := s[:0]
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
