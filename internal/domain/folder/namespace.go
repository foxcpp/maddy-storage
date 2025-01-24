package folder

type Namespace struct {
	AppendLimit      uint32 `json:"append_limit"`
	MaxMessagesCount uint32 `json:"max_messages_count"`
	MaxStorageBytes  uint64 `json:"max_storage_bytes"`
}
