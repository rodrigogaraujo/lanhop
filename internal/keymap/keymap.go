package keymap

type Translator struct {
	SwapAltAndSuper bool
}

func (t Translator) ToMacOS(linuxKey uint16) (macOSKey uint16, mapped bool) {
	macOSKey, mapped = macOSByLinux[t.swapAltAndSuper(linuxKey)]
	return macOSKey, mapped
}

func (t Translator) ToLinux(macOSKey uint16) (linuxKey uint16, mapped bool) {
	linuxKey, mapped = linuxByMacOS[macOSKey]
	if !mapped {
		return 0, false
	}
	return t.swapAltAndSuper(linuxKey), true
}

func (t Translator) swapAltAndSuper(linuxKey uint16) uint16 {
	if !t.SwapAltAndSuper {
		return linuxKey
	}
	if counterpart, isAltOrSuper := altAndSuperCounterparts[linuxKey]; isAltOrSuper {
		return counterpart
	}
	return linuxKey
}

var altAndSuperCounterparts = map[uint16]uint16{
	keyLeftAlt:   keyLeftMeta,
	keyLeftMeta:  keyLeftAlt,
	keyRightAlt:  keyRightMeta,
	keyRightMeta: keyRightAlt,
}

var macOSByLinux, linuxByMacOS = indexBothWays(physicalKeys)

func indexBothWays(keys []physicalKey) (macOSByLinux, linuxByMacOS map[uint16]uint16) {
	macOSByLinux = make(map[uint16]uint16, len(keys))
	linuxByMacOS = make(map[uint16]uint16, len(keys))
	for _, key := range keys {
		macOSByLinux[key.linux] = key.macOS
		linuxByMacOS[key.macOS] = key.linux
	}
	return macOSByLinux, linuxByMacOS
}

type physicalKey struct {
	linux uint16
	macOS uint16
}

var physicalKeys = []physicalKey{
	{linux: keyA, macOS: vkANSIA},
	{linux: keyB, macOS: vkANSIB},
	{linux: keyC, macOS: vkANSIC},
	{linux: keyD, macOS: vkANSID},
	{linux: keyE, macOS: vkANSIE},
	{linux: keyF, macOS: vkANSIF},
	{linux: keyG, macOS: vkANSIG},
	{linux: keyH, macOS: vkANSIH},
	{linux: keyI, macOS: vkANSII},
	{linux: keyJ, macOS: vkANSIJ},
	{linux: keyK, macOS: vkANSIK},
	{linux: keyL, macOS: vkANSIL},
	{linux: keyM, macOS: vkANSIM},
	{linux: keyN, macOS: vkANSIN},
	{linux: keyO, macOS: vkANSIO},
	{linux: keyP, macOS: vkANSIP},
	{linux: keyQ, macOS: vkANSIQ},
	{linux: keyR, macOS: vkANSIR},
	{linux: keyS, macOS: vkANSIS},
	{linux: keyT, macOS: vkANSIT},
	{linux: keyU, macOS: vkANSIU},
	{linux: keyV, macOS: vkANSIV},
	{linux: keyW, macOS: vkANSIW},
	{linux: keyX, macOS: vkANSIX},
	{linux: keyY, macOS: vkANSIY},
	{linux: keyZ, macOS: vkANSIZ},
	{linux: key1, macOS: vkANSI1},
	{linux: key2, macOS: vkANSI2},
	{linux: key3, macOS: vkANSI3},
	{linux: key4, macOS: vkANSI4},
	{linux: key5, macOS: vkANSI5},
	{linux: key6, macOS: vkANSI6},
	{linux: key7, macOS: vkANSI7},
	{linux: key8, macOS: vkANSI8},
	{linux: key9, macOS: vkANSI9},
	{linux: key0, macOS: vkANSI0},
	{linux: keyMinus, macOS: vkANSIMinus},
	{linux: keyEqual, macOS: vkANSIEqual},
	{linux: keyLeftBrace, macOS: vkANSILeftBracket},
	{linux: keyRightBrace, macOS: vkANSIRightBracket},
	{linux: keySemicolon, macOS: vkANSISemicolon},
	{linux: keyApostrophe, macOS: vkANSIQuote},
	{linux: keyGrave, macOS: vkANSIGrave},
	{linux: keyBackslash, macOS: vkANSIBackslash},
	{linux: keyComma, macOS: vkANSIComma},
	{linux: keyDot, macOS: vkANSIPeriod},
	{linux: keySlash, macOS: vkANSISlash},
	{linux: key102nd, macOS: vkISOSection},
	{linux: keyEsc, macOS: vkEscape},
	{linux: keyTab, macOS: vkTab},
	{linux: keyCapsLock, macOS: vkCapsLock},
	{linux: keySpace, macOS: vkSpace},
	{linux: keyEnter, macOS: vkReturn},
	{linux: keyBackspace, macOS: vkDelete},
	{linux: keyDelete, macOS: vkForwardDelete},
	{linux: keyInsert, macOS: vkHelp},
	{linux: keyHome, macOS: vkHome},
	{linux: keyEnd, macOS: vkEnd},
	{linux: keyPageUp, macOS: vkPageUp},
	{linux: keyPageDown, macOS: vkPageDown},
	{linux: keyLeft, macOS: vkLeftArrow},
	{linux: keyRight, macOS: vkRightArrow},
	{linux: keyUp, macOS: vkUpArrow},
	{linux: keyDown, macOS: vkDownArrow},
	{linux: keyF1, macOS: vkF1},
	{linux: keyF2, macOS: vkF2},
	{linux: keyF3, macOS: vkF3},
	{linux: keyF4, macOS: vkF4},
	{linux: keyF5, macOS: vkF5},
	{linux: keyF6, macOS: vkF6},
	{linux: keyF7, macOS: vkF7},
	{linux: keyF8, macOS: vkF8},
	{linux: keyF9, macOS: vkF9},
	{linux: keyF10, macOS: vkF10},
	{linux: keyF11, macOS: vkF11},
	{linux: keyF12, macOS: vkF12},
	{linux: keyF13, macOS: vkF13},
	{linux: keyF14, macOS: vkF14},
	{linux: keyF15, macOS: vkF15},
	{linux: keyF16, macOS: vkF16},
	{linux: keyF17, macOS: vkF17},
	{linux: keyF18, macOS: vkF18},
	{linux: keyF19, macOS: vkF19},
	{linux: keyF20, macOS: vkF20},
	{linux: keyKP0, macOS: vkANSIKeypad0},
	{linux: keyKP1, macOS: vkANSIKeypad1},
	{linux: keyKP2, macOS: vkANSIKeypad2},
	{linux: keyKP3, macOS: vkANSIKeypad3},
	{linux: keyKP4, macOS: vkANSIKeypad4},
	{linux: keyKP5, macOS: vkANSIKeypad5},
	{linux: keyKP6, macOS: vkANSIKeypad6},
	{linux: keyKP7, macOS: vkANSIKeypad7},
	{linux: keyKP8, macOS: vkANSIKeypad8},
	{linux: keyKP9, macOS: vkANSIKeypad9},
	{linux: keyKPDot, macOS: vkANSIKeypadDecimal},
	{linux: keyKPEnter, macOS: vkANSIKeypadEnter},
	{linux: keyKPEqual, macOS: vkANSIKeypadEquals},
	{linux: keyKPPlus, macOS: vkANSIKeypadPlus},
	{linux: keyKPMinus, macOS: vkANSIKeypadMinus},
	{linux: keyKPAsterisk, macOS: vkANSIKeypadMultiply},
	{linux: keyKPSlash, macOS: vkANSIKeypadDivide},
	{linux: keyNumLock, macOS: vkANSIKeypadClear},
	{linux: keyLeftCtrl, macOS: vkControl},
	{linux: keyRightCtrl, macOS: vkRightControl},
	{linux: keyLeftShift, macOS: vkShift},
	{linux: keyRightShift, macOS: vkRightShift},
	{linux: keyLeftAlt, macOS: vkOption},
	{linux: keyRightAlt, macOS: vkRightOption},
	{linux: keyLeftMeta, macOS: vkCommand},
	{linux: keyRightMeta, macOS: vkRightCommand},
	{linux: keyMute, macOS: vkMute},
	{linux: keyVolumeDown, macOS: vkVolumeDown},
	{linux: keyVolumeUp, macOS: vkVolumeUp},
}
