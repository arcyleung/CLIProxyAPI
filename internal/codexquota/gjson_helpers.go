package codexquota

import "github.com/tidwall/gjson"

func gjsonGet(body []byte, path string) string {
	return gjson.GetBytes(body, path).String()
}

func gjsonExists(body []byte, path string) bool {
	return gjson.GetBytes(body, path).Exists()
}

func gjsonInt(body []byte, path string) int64 {
	return gjson.GetBytes(body, path).Int()
}
