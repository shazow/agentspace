package units

const bytesPerMiB int64 = 1024 * 1024

type MiB int

func (m MiB) Bytes() int64 {
	return int64(m) * bytesPerMiB
}

func (m MiB) Int() int {
	return int(m)
}
