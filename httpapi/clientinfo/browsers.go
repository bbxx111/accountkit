package clientinfo

const browserSafari = "Safari"

// browserDescription separates canonical UA output from external brand aliases.
type browserDescription struct {
	name, brand string
	rank        int
}

var browsers = [...]browserDescription{
	{"Chrome", "Google Chrome", 2},
	{"Chromium", "Chromium", 1},
	{browserSafari, "", 0},
	{"Firefox", "", 0},
	{"Edge", "Microsoft Edge", 4},
	{"Opera", "Opera", 3},
	{"Opera Mini", "", 0},
	{"Opera Touch", "", 0},
	{"Internet Explorer", "", 0},
	{"Samsung Browser", "", 0},
	{"Vivaldi", "", 0},
	{"Huawei Browser", "", 0},
}
