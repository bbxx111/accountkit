package clientinfo

// Header names identify explicit descriptions and UA Client Hints.
const (
	headerClientType           = "X-Client-Type"
	headerClientOSName         = "X-Client-OS-Name"
	headerClientOSVersion      = "X-Client-OS-Version"
	headerClientBrowserName    = "X-Client-Browser-Name"
	headerClientBrowserVersion = "X-Client-Browser-Version"
	headerClientAppName        = "X-Client-App-Name"
	headerClientAppVersion     = "X-Client-App-Version"
	headerDeviceModel          = "X-Device-Model"
	headerDeviceType           = "X-Device-Type"
	headerCHUA                 = "Sec-CH-UA"
	headerCHUAFullVersionList  = "Sec-CH-UA-Full-Version-List"
	headerCHUAPlatform         = "Sec-CH-UA-Platform"
	headerCHUAPlatformVersion  = "Sec-CH-UA-Platform-Version"
	headerCHUAModel            = "Sec-CH-UA-Model"
	headerCHUAMobile           = "Sec-CH-UA-Mobile"
	hintHeaderPrefix           = "sec-ch-ua"
	hintVersionParameter       = "v"
)
