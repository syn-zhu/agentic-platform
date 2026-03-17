package lifecycle

import "fmt"

func FormatRequestData(method, url string, headers map[string]string) map[string]any {
	return map[string]any{"method": method, "url": url, "headers": headers}
}

func FormatResponseData(statusCode int, headers map[string]string) map[string]any {
	return map[string]any{"status_code": statusCode, "headers": headers}
}

func FormatError(err error) map[string]any {
	return map[string]any{"error": fmt.Sprintf("%v", err)}
}
