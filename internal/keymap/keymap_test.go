package keymap

import (
	"fmt"
	"testing"
)

const everyCode = 1 << 16

var bothSettings = []Translator{{SwapAltAndSuper: false}, {SwapAltAndSuper: true}}

func settingName(translator Translator) string {
	return fmt.Sprintf("swap alt and super %t", translator.SwapAltAndSuper)
}

var headerCodes = []struct {
	linuxName string
	linux     uint16
	macOSName string
	macOS     uint16
}{
	{"KEY_A", 30, "kVK_ANSI_A", 0x00},
	{"KEY_B", 48, "kVK_ANSI_B", 0x0B},
	{"KEY_C", 46, "kVK_ANSI_C", 0x08},
	{"KEY_D", 32, "kVK_ANSI_D", 0x02},
	{"KEY_E", 18, "kVK_ANSI_E", 0x0E},
	{"KEY_F", 33, "kVK_ANSI_F", 0x03},
	{"KEY_G", 34, "kVK_ANSI_G", 0x05},
	{"KEY_H", 35, "kVK_ANSI_H", 0x04},
	{"KEY_I", 23, "kVK_ANSI_I", 0x22},
	{"KEY_J", 36, "kVK_ANSI_J", 0x26},
	{"KEY_K", 37, "kVK_ANSI_K", 0x28},
	{"KEY_L", 38, "kVK_ANSI_L", 0x25},
	{"KEY_M", 50, "kVK_ANSI_M", 0x2E},
	{"KEY_N", 49, "kVK_ANSI_N", 0x2D},
	{"KEY_O", 24, "kVK_ANSI_O", 0x1F},
	{"KEY_P", 25, "kVK_ANSI_P", 0x23},
	{"KEY_Q", 16, "kVK_ANSI_Q", 0x0C},
	{"KEY_R", 19, "kVK_ANSI_R", 0x0F},
	{"KEY_S", 31, "kVK_ANSI_S", 0x01},
	{"KEY_T", 20, "kVK_ANSI_T", 0x11},
	{"KEY_U", 22, "kVK_ANSI_U", 0x20},
	{"KEY_V", 47, "kVK_ANSI_V", 0x09},
	{"KEY_W", 17, "kVK_ANSI_W", 0x0D},
	{"KEY_X", 45, "kVK_ANSI_X", 0x07},
	{"KEY_Y", 21, "kVK_ANSI_Y", 0x10},
	{"KEY_Z", 44, "kVK_ANSI_Z", 0x06},
	{"KEY_1", 2, "kVK_ANSI_1", 0x12},
	{"KEY_2", 3, "kVK_ANSI_2", 0x13},
	{"KEY_3", 4, "kVK_ANSI_3", 0x14},
	{"KEY_4", 5, "kVK_ANSI_4", 0x15},
	{"KEY_5", 6, "kVK_ANSI_5", 0x17},
	{"KEY_6", 7, "kVK_ANSI_6", 0x16},
	{"KEY_7", 8, "kVK_ANSI_7", 0x1A},
	{"KEY_8", 9, "kVK_ANSI_8", 0x1C},
	{"KEY_9", 10, "kVK_ANSI_9", 0x19},
	{"KEY_0", 11, "kVK_ANSI_0", 0x1D},
	{"KEY_MINUS", 12, "kVK_ANSI_Minus", 0x1B},
	{"KEY_EQUAL", 13, "kVK_ANSI_Equal", 0x18},
	{"KEY_LEFTBRACE", 26, "kVK_ANSI_LeftBracket", 0x21},
	{"KEY_RIGHTBRACE", 27, "kVK_ANSI_RightBracket", 0x1E},
	{"KEY_SEMICOLON", 39, "kVK_ANSI_Semicolon", 0x29},
	{"KEY_APOSTROPHE", 40, "kVK_ANSI_Quote", 0x27},
	{"KEY_GRAVE", 41, "kVK_ANSI_Grave", 0x32},
	{"KEY_BACKSLASH", 43, "kVK_ANSI_Backslash", 0x2A},
	{"KEY_COMMA", 51, "kVK_ANSI_Comma", 0x2B},
	{"KEY_DOT", 52, "kVK_ANSI_Period", 0x2F},
	{"KEY_SLASH", 53, "kVK_ANSI_Slash", 0x2C},
	{"KEY_102ND", 86, "kVK_ISO_Section", 0x0A},
	{"KEY_ESC", 1, "kVK_Escape", 0x35},
	{"KEY_TAB", 15, "kVK_Tab", 0x30},
	{"KEY_CAPSLOCK", 58, "kVK_CapsLock", 0x39},
	{"KEY_SPACE", 57, "kVK_Space", 0x31},
	{"KEY_ENTER", 28, "kVK_Return", 0x24},
	{"KEY_BACKSPACE", 14, "kVK_Delete", 0x33},
	{"KEY_DELETE", 111, "kVK_ForwardDelete", 0x75},
	{"KEY_INSERT", 110, "kVK_Help", 0x72},
	{"KEY_HOME", 102, "kVK_Home", 0x73},
	{"KEY_END", 107, "kVK_End", 0x77},
	{"KEY_PAGEUP", 104, "kVK_PageUp", 0x74},
	{"KEY_PAGEDOWN", 109, "kVK_PageDown", 0x79},
	{"KEY_LEFT", 105, "kVK_LeftArrow", 0x7B},
	{"KEY_RIGHT", 106, "kVK_RightArrow", 0x7C},
	{"KEY_UP", 103, "kVK_UpArrow", 0x7E},
	{"KEY_DOWN", 108, "kVK_DownArrow", 0x7D},
	{"KEY_F1", 59, "kVK_F1", 0x7A},
	{"KEY_F2", 60, "kVK_F2", 0x78},
	{"KEY_F3", 61, "kVK_F3", 0x63},
	{"KEY_F4", 62, "kVK_F4", 0x76},
	{"KEY_F5", 63, "kVK_F5", 0x60},
	{"KEY_F6", 64, "kVK_F6", 0x61},
	{"KEY_F7", 65, "kVK_F7", 0x62},
	{"KEY_F8", 66, "kVK_F8", 0x64},
	{"KEY_F9", 67, "kVK_F9", 0x65},
	{"KEY_F10", 68, "kVK_F10", 0x6D},
	{"KEY_F11", 87, "kVK_F11", 0x67},
	{"KEY_F12", 88, "kVK_F12", 0x6F},
	{"KEY_F13", 183, "kVK_F13", 0x69},
	{"KEY_F14", 184, "kVK_F14", 0x6B},
	{"KEY_F15", 185, "kVK_F15", 0x71},
	{"KEY_F16", 186, "kVK_F16", 0x6A},
	{"KEY_F17", 187, "kVK_F17", 0x40},
	{"KEY_F18", 188, "kVK_F18", 0x4F},
	{"KEY_F19", 189, "kVK_F19", 0x50},
	{"KEY_F20", 190, "kVK_F20", 0x5A},
	{"KEY_KP0", 82, "kVK_ANSI_Keypad0", 0x52},
	{"KEY_KP1", 79, "kVK_ANSI_Keypad1", 0x53},
	{"KEY_KP2", 80, "kVK_ANSI_Keypad2", 0x54},
	{"KEY_KP3", 81, "kVK_ANSI_Keypad3", 0x55},
	{"KEY_KP4", 75, "kVK_ANSI_Keypad4", 0x56},
	{"KEY_KP5", 76, "kVK_ANSI_Keypad5", 0x57},
	{"KEY_KP6", 77, "kVK_ANSI_Keypad6", 0x58},
	{"KEY_KP7", 71, "kVK_ANSI_Keypad7", 0x59},
	{"KEY_KP8", 72, "kVK_ANSI_Keypad8", 0x5B},
	{"KEY_KP9", 73, "kVK_ANSI_Keypad9", 0x5C},
	{"KEY_KPDOT", 83, "kVK_ANSI_KeypadDecimal", 0x41},
	{"KEY_KPENTER", 96, "kVK_ANSI_KeypadEnter", 0x4C},
	{"KEY_KPEQUAL", 117, "kVK_ANSI_KeypadEquals", 0x51},
	{"KEY_KPPLUS", 78, "kVK_ANSI_KeypadPlus", 0x45},
	{"KEY_KPMINUS", 74, "kVK_ANSI_KeypadMinus", 0x4E},
	{"KEY_KPASTERISK", 55, "kVK_ANSI_KeypadMultiply", 0x43},
	{"KEY_KPSLASH", 98, "kVK_ANSI_KeypadDivide", 0x4B},
	{"KEY_NUMLOCK", 69, "kVK_ANSI_KeypadClear", 0x47},
	{"KEY_LEFTCTRL", 29, "kVK_Control", 0x3B},
	{"KEY_RIGHTCTRL", 97, "kVK_RightControl", 0x3E},
	{"KEY_LEFTSHIFT", 42, "kVK_Shift", 0x38},
	{"KEY_RIGHTSHIFT", 54, "kVK_RightShift", 0x3C},
	{"KEY_LEFTALT", 56, "kVK_Option", 0x3A},
	{"KEY_RIGHTALT", 100, "kVK_RightOption", 0x3D},
	{"KEY_LEFTMETA", 125, "kVK_Command", 0x37},
	{"KEY_RIGHTMETA", 126, "kVK_RightCommand", 0x36},
	{"KEY_MUTE", 113, "kVK_Mute", 0x4A},
	{"KEY_VOLUMEDOWN", 114, "kVK_VolumeDown", 0x49},
	{"KEY_VOLUMEUP", 115, "kVK_VolumeUp", 0x48},
}

func TestEveryKeyOfAFullKeyboardTranslatesToItsCodeInTheOtherSystemsHeader(t *testing.T) {
	translator := Translator{}
	for _, key := range headerCodes {
		t.Run(key.linuxName, func(t *testing.T) {
			if macOS, mapped := translator.ToMacOS(key.linux); !mapped || macOS != key.macOS {
				t.Errorf("ToMacOS(%s = %d) = %#02x, %t, want %s = %#02x", key.linuxName, key.linux, macOS, mapped, key.macOSName, key.macOS)
			}
			if linux, mapped := translator.ToLinux(key.macOS); !mapped || linux != key.linux {
				t.Errorf("ToLinux(%s = %#02x) = %d, %t, want %s = %d", key.macOSName, key.macOS, linux, mapped, key.linuxName, key.linux)
			}
		})
	}
}

func TestOnlyTheKeysOfAFullKeyboardAreMapped(t *testing.T) {
	for _, translator := range bothSettings {
		t.Run(settingName(translator), func(t *testing.T) {
			mappedLinux, mappedMacOS := 0, 0
			for code := 0; code < everyCode; code++ {
				if _, mapped := translator.ToMacOS(uint16(code)); mapped {
					mappedLinux++
				}
				if _, mapped := translator.ToLinux(uint16(code)); mapped {
					mappedMacOS++
				}
			}
			if mappedLinux != len(headerCodes) {
				t.Errorf("%d Linux codes are mapped, want %d", mappedLinux, len(headerCodes))
			}
			if mappedMacOS != len(headerCodes) {
				t.Errorf("%d macOS codes are mapped, want %d", mappedMacOS, len(headerCodes))
			}
		})
	}
}

func TestALinuxKeyWithNoCounterpartOnMacOSIsNotMapped(t *testing.T) {
	keys := []struct {
		name string
		code uint16
	}{
		{"KEY_RESERVED", 0},
		{"KEY_SCROLLLOCK", 70},
		{"KEY_SYSRQ", 99},
		{"KEY_PAUSE", 119},
		{"KEY_KPCOMMA", 121},
		{"KEY_KPJPCOMMA", 95},
		{"KEY_YEN", 124},
		{"KEY_RO", 89},
		{"KEY_COMPOSE", 127},
		{"KEY_POWER", 116},
		{"KEY_F21", 191},
		{"KEY_F24", 194},
		{"KEY_MICMUTE", 248},
		{"KEY_FN", 464},
		{"BTN_LEFT", 0x110},
		{"KEY_MAX", 0x2ff},
		{"the highest code", 0xFFFF},
	}
	for _, translator := range bothSettings {
		for _, key := range keys {
			t.Run(settingName(translator)+"/"+key.name, func(t *testing.T) {
				if macOS, mapped := translator.ToMacOS(key.code); mapped {
					t.Errorf("ToMacOS(%s = %d) = %#02x, want not mapped", key.name, key.code, macOS)
				}
			})
		}
	}
}

func TestAMacOSKeyWithNoCounterpartOnLinuxIsNotMapped(t *testing.T) {
	keys := []struct {
		name string
		code uint16
	}{
		{"kVK_Function", 0x3F},
		{"kVK_ContextualMenu", 0x6E},
		{"kVK_JIS_Yen", 0x5D},
		{"kVK_JIS_Underscore", 0x5E},
		{"kVK_JIS_KeypadComma", 0x5F},
		{"kVK_JIS_Eisu", 0x66},
		{"kVK_JIS_Kana", 0x68},
		{"unassigned 0x34", 0x34},
		{"unassigned 0x7F", 0x7F},
		{"first code above the table", 0x80},
		{"the highest code", 0xFFFF},
	}
	for _, translator := range bothSettings {
		for _, key := range keys {
			t.Run(settingName(translator)+"/"+key.name, func(t *testing.T) {
				if linux, mapped := translator.ToLinux(key.code); mapped {
					t.Errorf("ToLinux(%s = %#02x) = %d, want not mapped", key.name, key.code, linux)
				}
			})
		}
	}
}

func TestEveryMappedKeySurvivesARoundTripInBothDirections(t *testing.T) {
	for _, translator := range bothSettings {
		t.Run(settingName(translator), func(t *testing.T) {
			for code := 0; code < everyCode; code++ {
				linux := uint16(code)
				if macOS, mapped := translator.ToMacOS(linux); mapped {
					if back, mappedBack := translator.ToLinux(macOS); !mappedBack || back != linux {
						t.Errorf("Linux %d became macOS %#02x and came back as %d, %t", linux, macOS, back, mappedBack)
					}
				}
				macOS := uint16(code)
				if linux, mapped := translator.ToLinux(macOS); mapped {
					if back, mappedBack := translator.ToMacOS(linux); !mappedBack || back != macOS {
						t.Errorf("macOS %#02x became Linux %d and came back as %#02x, %t", macOS, linux, back, mappedBack)
					}
				}
			}
		})
	}
}

func TestNoTwoKeysTranslateToTheSameKey(t *testing.T) {
	for _, translator := range bothSettings {
		t.Run(settingName(translator), func(t *testing.T) {
			linuxSources := make(map[uint16]uint16)
			macOSSources := make(map[uint16]uint16)
			for code := 0; code < everyCode; code++ {
				source := uint16(code)
				if macOS, mapped := translator.ToMacOS(source); mapped {
					if other, taken := linuxSources[macOS]; taken {
						t.Errorf("Linux %d and Linux %d both translate to macOS %#02x", other, source, macOS)
					}
					linuxSources[macOS] = source
				}
				if linux, mapped := translator.ToLinux(source); mapped {
					if other, taken := macOSSources[linux]; taken {
						t.Errorf("macOS %#02x and macOS %#02x both translate to Linux %d", other, source, linux)
					}
					macOSSources[linux] = source
				}
			}
		})
	}
}

var modifiers = []struct {
	linuxName    string
	linux        uint16
	byMeaning    string
	macOS        uint16
	swappedName  string
	macOSSwapped uint16
}{
	{"KEY_LEFTCTRL", 29, "kVK_Control", 0x3B, "kVK_Control", 0x3B},
	{"KEY_RIGHTCTRL", 97, "kVK_RightControl", 0x3E, "kVK_RightControl", 0x3E},
	{"KEY_LEFTSHIFT", 42, "kVK_Shift", 0x38, "kVK_Shift", 0x38},
	{"KEY_RIGHTSHIFT", 54, "kVK_RightShift", 0x3C, "kVK_RightShift", 0x3C},
	{"KEY_LEFTALT", 56, "kVK_Option", 0x3A, "kVK_Command", 0x37},
	{"KEY_RIGHTALT", 100, "kVK_RightOption", 0x3D, "kVK_RightCommand", 0x36},
	{"KEY_LEFTMETA", 125, "kVK_Command", 0x37, "kVK_Option", 0x3A},
	{"KEY_RIGHTMETA", 126, "kVK_RightCommand", 0x36, "kVK_RightOption", 0x3D},
}

func TestModifiersTranslateByMeaning(t *testing.T) {
	translator := Translator{}
	for _, modifier := range modifiers {
		t.Run(modifier.linuxName, func(t *testing.T) {
			if macOS, mapped := translator.ToMacOS(modifier.linux); !mapped || macOS != modifier.macOS {
				t.Errorf("ToMacOS(%s) = %#02x, %t, want %s = %#02x", modifier.linuxName, macOS, mapped, modifier.byMeaning, modifier.macOS)
			}
			if linux, mapped := translator.ToLinux(modifier.macOS); !mapped || linux != modifier.linux {
				t.Errorf("ToLinux(%s) = %d, %t, want %s = %d", modifier.byMeaning, linux, mapped, modifier.linuxName, modifier.linux)
			}
		})
	}
}

func TestSwappingAltAndSuperExchangesOptionAndCommandInBothDirections(t *testing.T) {
	translator := Translator{SwapAltAndSuper: true}
	for _, modifier := range modifiers {
		t.Run(modifier.linuxName, func(t *testing.T) {
			if macOS, mapped := translator.ToMacOS(modifier.linux); !mapped || macOS != modifier.macOSSwapped {
				t.Errorf("ToMacOS(%s) = %#02x, %t, want %s = %#02x", modifier.linuxName, macOS, mapped, modifier.swappedName, modifier.macOSSwapped)
			}
			if linux, mapped := translator.ToLinux(modifier.macOSSwapped); !mapped || linux != modifier.linux {
				t.Errorf("ToLinux(%s) = %d, %t, want %s = %d", modifier.swappedName, linux, mapped, modifier.linuxName, modifier.linux)
			}
		})
	}
}

func TestSwappingAltAndSuperLeavesEveryOtherKeyAlone(t *testing.T) {
	plain := Translator{}
	swapping := Translator{SwapAltAndSuper: true}
	linuxAltAndSuper := map[uint16]bool{56: true, 100: true, 125: true, 126: true}
	macOSOptionAndCommand := map[uint16]bool{0x3A: true, 0x3D: true, 0x37: true, 0x36: true}
	for code := 0; code < everyCode; code++ {
		key := uint16(code)
		if !linuxAltAndSuper[key] {
			want, wantMapped := plain.ToMacOS(key)
			if got, mapped := swapping.ToMacOS(key); got != want || mapped != wantMapped {
				t.Errorf("with the swap ToMacOS(%d) = %#02x, %t, want %#02x, %t", key, got, mapped, want, wantMapped)
			}
		}
		if !macOSOptionAndCommand[key] {
			want, wantMapped := plain.ToLinux(key)
			if got, mapped := swapping.ToLinux(key); got != want || mapped != wantMapped {
				t.Errorf("with the swap ToLinux(%#02x) = %d, %t, want %d, %t", key, got, mapped, want, wantMapped)
			}
		}
	}
}
