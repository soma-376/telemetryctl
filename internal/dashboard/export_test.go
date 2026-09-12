package dashboard

// 화면 간 통합 테스트가 기존 픽스처를 공유한다. 제품 빌드에는 포함되지 않는다.

import (
	"testing"

	"github.com/your-org/pulsemetry/internal/store"
)

var TestAssertSnakeCaseTags = assertSnakeCaseTags
var TestCodex = codex
var TestContainsString = containsString
var TestCrossDay = crossDay
var TestFileChange = fileChange

type TestFixture = fixture

var TestLlmRecord = llmRecord

type TestLlmSpec = llmSpec

var TestMustTime = mustTime
var TestNewFixture = newFixture
var TestNewSession = newSession
var TestPromptRecord = promptRecord
var TestRunning = running
var TestSeedCleanDay = seedCleanDay
var TestSeoul = seoul
var TestShortName = shortName
var TestNow = testNow
var TestTitle = title
var TestToolRecord = toolRecord

type TestToolSpec = toolSpec

var TestUtc = utc
var TestVendorClaude = vendorClaude
var TestVendorCodex = vendorCodex

const TestWorkspaceA = workspaceA

var TestWorkspaceB = workspaceB

func (f *fixture) TestReader() *Reader                    { return f.reader }
func (f *fixture) TestPath() string                       { return f.path }
func (f *fixture) TestDB() *store.DB                      { return f.db }
func (f *fixture) TestDir() string                        { return f.dir }
func (f *fixture) TestT() *testing.T                      { return f.t }
func (f *fixture) TestWrite(b store.Batch)                { f.write(b) }
func (f *fixture) TestSessionID(vendor, key string) int64 { return f.sessionID(vendor, key) }

const TestDefaultSessionTurns = defaultSessionTurns

const TestMaxToolEvents = maxToolEvents
const TestMaxFileTimeline = maxFileTimeline
