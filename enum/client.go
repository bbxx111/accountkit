package enum

// ClientType 区分浏览器与原生客户端，不表示设备或操作系统。
type ClientType int16

const (
	ClientTypeUnspecified ClientType = iota
	ClientWeb
	ClientNative
)

var clientTypeNames = [...]string{"UNSPECIFIED", "WEB", "NATIVE"}

func (v ClientType) String() string { return name(clientTypeNames[:], int(v)) }
func (v ClientType) Valid() bool    { return v == ClientWeb || v == ClientNative }
func ParseClientType(v string) (ClientType, error) {
	i, err := parse(clientTypeNames[:], v, "ClientType")
	return ClientType(i), err
}

// DeviceType 是自报或本地解析的设备类别，不是可信设备证明。
type DeviceType int16

const (
	DeviceTypeUnspecified DeviceType = iota
	DeviceDesktop
	DevicePhone
	DeviceTablet
)

var deviceTypeNames = [...]string{"UNSPECIFIED", "DESKTOP", "PHONE", "TABLET"}

func (v DeviceType) String() string { return name(deviceTypeNames[:], int(v)) }
func (v DeviceType) Valid() bool    { return v == DeviceDesktop || v == DevicePhone || v == DeviceTablet }
func ParseDeviceType(v string) (DeviceType, error) {
	i, err := parse(deviceTypeNames[:], v, "DeviceType")
	return DeviceType(i), err
}

// ClientInfoSnapshotKind 区分首次环境与最近认证活动观察值。
type ClientInfoSnapshotKind int16

const (
	ClientInfoSnapshotKindUnspecified ClientInfoSnapshotKind = iota
	ClientSnapshotInitial
	ClientSnapshotLatest
)

var clientInfoSnapshotKindNames = [...]string{"UNSPECIFIED", "INITIAL", "LATEST"}

func (v ClientInfoSnapshotKind) String() string { return name(clientInfoSnapshotKindNames[:], int(v)) }
func (v ClientInfoSnapshotKind) Valid() bool {
	return v == ClientSnapshotInitial || v == ClientSnapshotLatest
}
func ParseClientInfoSnapshotKind(v string) (ClientInfoSnapshotKind, error) {
	i, err := parse(clientInfoSnapshotKindNames[:], v, "ClientInfoSnapshotKind")
	return ClientInfoSnapshotKind(i), err
}
