package valueobject

// MaskNIK masks a 16-digit NIK for display, showing the first 4 and last 2 digits.
// Example: "3171000000000022" → "3171**********22"
// Returns the input unchanged for NIKs that are not exactly 16 characters.
func MaskNIK(nik string) string {
	if len(nik) != 16 {
		return nik
	}
	return nik[:4] + "**********" + nik[14:]
}
