package version

var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

func String() string {
	return Version
}
