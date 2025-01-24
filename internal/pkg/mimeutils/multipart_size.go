package mimeutils

// MultipartOctetSize counts how many bytes multipart separators
func MultipartOctetSize(partsCount int, boundary string) uint32 {
	// -2 is because we count \r\n ending empty line after header as part of header.
	return uint32(partsCount*len("\r\n--"+boundary+"\r\n") + len("\r\n--"+boundary+"--\r\n") - 2)
}

func MultipartLineCount(partsCount int) int64 {
	return int64(partsCount + 1)
}
