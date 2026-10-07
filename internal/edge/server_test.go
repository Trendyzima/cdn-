package edge
import "testing"
func TestCleanAssetPath(t *testing.T){tests:=[]struct{in string;ok bool;want string}{{"/v1/live/channel/index.m3u8",true,"live/channel/index.m3u8"},{"/v1/live/../secret",false,""},{"/v1/../../secret",false,""},{"/v1/",false,""}};for _,tt:=range tests{got,ok:=cleanAssetPath(tt.in);if ok!=tt.ok||got!=tt.want{t.Fatalf("%q => %q,%v",tt.in,got,ok)}}}
