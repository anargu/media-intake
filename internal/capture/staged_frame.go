package capture

type StagedFrame struct {
	storage     *FileSystemStorage
	stagingPath string
	size        int64
	sha256      string
}
