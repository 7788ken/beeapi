package contentbackupworker

import "os"

// fileReader 让测试无法替换 os.Open 之外的打开行为时仍有最小抽象面。
type fileReader struct {
	file *os.File
}

func openFileReader(path string) (*fileReader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &fileReader{file: file}, nil
}

func (r *fileReader) Read(p []byte) (int, error) { return r.file.Read(p) }

func (r *fileReader) Close() error { return r.file.Close() }
