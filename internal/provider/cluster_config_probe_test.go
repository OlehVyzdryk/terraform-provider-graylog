package provider

// Classes that exist across Graylog 5.x, 6.x and 7.x. The set of fields a
// cluster configuration class requires differs between versions, so tests
// discover a class that already holds a document and reuse that document
// verbatim rather than hard-coding a shape.
//
// Deliberately free of a build tag: both the integration and the acceptance
// suites rely on it.
var clusterConfigProbeClasses = []string{
	"org.graylog2.indexer.searches.SearchesClusterConfig",
	"org.graylog2.messageprocessors.MessageProcessorsConfig",
	"org.graylog2.indexer.indexset.DefaultIndexSetConfig",
}

// A class name inside the default safe_classes prefixes that Graylog cannot
// resolve, used to exercise the not-found paths.
const clusterConfigAbsentClass = "org.graylog2.TerraformProviderAbsentClass"
