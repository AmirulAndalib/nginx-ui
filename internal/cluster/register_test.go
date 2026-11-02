package cluster

import (
	"context"
	"strings"
	"testing"

	"github.com/0xJacky/Nginx-UI/internal/nodeauth"
	"github.com/0xJacky/Nginx-UI/internal/testdb"
	"github.com/0xJacky/Nginx-UI/model"
	"github.com/0xJacky/Nginx-UI/settings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupRegisterTest installs an in-memory database and the credential root
// key that legacy node secrets are encrypted with. nodes becomes the
// predefined cluster node list for the test.
func setupRegisterTest(t *testing.T, nodes ...string) *gorm.DB {
	t.Helper()

	originalNodes := settings.ClusterSettings.Node
	originalInstanceID := settings.NodeSettings.InstanceID
	originalCryptoSecret := settings.CryptoSettings.Secret
	originalDB := model.UseDB()
	t.Cleanup(func() {
		settings.ClusterSettings.Node = originalNodes
		settings.NodeSettings.InstanceID = originalInstanceID
		settings.CryptoSettings.Secret = originalCryptoSecret
		model.Use(originalDB)
	})

	settings.ClusterSettings.Node = nodes
	settings.NodeSettings.InstanceID = "11111111-1111-4111-8111-111111111111"
	settings.CryptoSettings.Secret = "cluster-register-test-root"

	database, err := gorm.Open(sqlite.Open(testdb.DSN(t)), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.Node{}))
	model.Use(database)
	return database
}

func listNodes(t *testing.T, database *gorm.DB) []model.Node {
	t.Helper()
	var nodes []model.Node
	require.NoError(t, database.Order("id").Find(&nodes).Error)
	return nodes
}

func TestRegisterPredefinedNodesStoresEncryptedLegacyCredential(t *testing.T) {
	database := setupRegisterTest(t,
		"http://10.0.0.1:9000?name=node1&node_secret=secret-one&enabled=true",
		"https://10.0.0.2/sub?name=node2&node_secret=secret-two",
	)

	RegisterPredefinedNodes(context.Background())

	nodes := listNodes(t, database)
	require.Len(t, nodes, 2)

	tests := []struct {
		node    model.Node
		name    string
		url     string
		secret  string
		enabled bool
	}{
		{node: nodes[0], name: "node1", url: "http://10.0.0.1:9000", secret: "secret-one", enabled: true},
		{node: nodes[1], name: "node2", url: "https://10.0.0.2/sub", secret: "secret-two", enabled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.name, tt.node.Name)
			assert.Equal(t, tt.url, tt.node.URL)
			assert.Equal(t, tt.enabled, tt.node.Enabled)
			assert.Equal(t, model.NodeAuthMethodLegacy, tt.node.AuthMethod)
			assert.Equal(t, model.NodeCredentialStatusActive, tt.node.CredentialStatus)
			assert.Empty(t, tt.node.Token, "the plaintext secret must not be stored")
			assert.NotContains(t, string(tt.node.EncryptedLegacySecret), tt.secret)

			secret, err := nodeauth.DecryptPrivateCredential(
				nodeauth.LegacyCredentialPurpose(tt.node.ID),
				tt.node.EncryptedLegacySecret,
			)
			require.NoError(t, err)
			assert.Equal(t, tt.secret, string(secret))
		})
	}
}

func TestRegisterPredefinedNodesKeepsExistingNodeWithSameURL(t *testing.T) {
	database := setupRegisterTest(t,
		"http://10.0.0.1:9000?name=renamed&node_secret=new-secret&enabled=true",
	)
	existing := model.Node{
		Name:             "original",
		URL:              "http://10.0.0.1:9000",
		AuthMethod:       model.NodeAuthMethodPaired,
		CredentialStatus: model.NodeCredentialStatusActive,
	}
	require.NoError(t, database.Create(&existing).Error)

	RegisterPredefinedNodes(context.Background())
	// Registering on every boot must stay idempotent.
	RegisterPredefinedNodes(context.Background())

	nodes := listNodes(t, database)
	require.Len(t, nodes, 1)
	assert.Equal(t, existing.ID, nodes[0].ID)
	assert.Equal(t, "original", nodes[0].Name)
	assert.Equal(t, model.NodeAuthMethodPaired, nodes[0].AuthMethod)
	assert.False(t, nodes[0].Enabled)
	assert.Empty(t, nodes[0].EncryptedLegacySecret)
}

func TestRegisterPredefinedNodesSkipsInvalidEntries(t *testing.T) {
	database := setupRegisterTest(t,
		"http://10.0.0.1:9000?node_secret=missing-name",
		"http://10.0.0.2:9000?name=missing-secret",
		"10.0.0.3:9000?name=no-scheme&node_secret=secret",
		"node.example?name=no-scheme-no-port&node_secret=secret",
		"?name=no-url&node_secret=secret",
		"ftp://10.0.0.5:21?name=wrong-scheme&node_secret=secret",
		"http://[::1?name=bad-host&node_secret=secret",
		"http://10.0.0.4:9000?name=valid&node_secret=valid-secret",
	)

	RegisterPredefinedNodes(context.Background())

	nodes := listNodes(t, database)
	require.Len(t, nodes, 1)
	assert.Equal(t, "valid", nodes[0].Name)
	assert.Equal(t, "http://10.0.0.4:9000", nodes[0].URL)
}

func TestRegisterPredefinedNodesRollsBackWhenCredentialCannotBeEncrypted(t *testing.T) {
	database := setupRegisterTest(t,
		"http://10.0.0.1:9000?name=node1&node_secret=secret-one",
	)
	// Without a root key the secret cannot be encrypted; a node row without
	// its credential must not be left behind.
	settings.CryptoSettings.Secret = ""

	RegisterPredefinedNodes(context.Background())

	assert.Empty(t, listNodes(t, database))
}

func TestRegisterPredefinedNodesWithoutDatabase(t *testing.T) {
	setupRegisterTest(t, "http://10.0.0.1:9000?name=node1&node_secret=secret-one")
	model.Use(nil)

	assert.NotPanics(t, func() {
		RegisterPredefinedNodes(context.Background())
	})
}

func TestSanitizePredefinedNodeURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "redacts the node secret",
			raw:  "http://10.0.0.1:9000?name=node1&node_secret=super-secret&enabled=true",
			want: "http://10.0.0.1:9000?enabled=true&name=node1&node_secret=%5BREDACTED%5D",
		},
		{
			name: "redacts an empty node secret",
			raw:  "http://10.0.0.1:9000?node_secret=",
			want: "http://10.0.0.1:9000?node_secret=%5BREDACTED%5D",
		},
		{
			name: "leaves a URL without a secret untouched",
			raw:  "http://10.0.0.1:9000/path?name=node1",
			want: "http://10.0.0.1:9000/path?name=node1",
		},
		{
			name: "hides an unparseable URL entirely",
			raw:  "http://[::1?node_secret=super-secret",
			want: "[invalid cluster node URL redacted]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizePredefinedNodeURL(tt.raw)
			assert.Equal(t, tt.want, got)
			assert.False(t, strings.Contains(got, "super-secret"), "secret leaked in %q", got)
		})
	}
}

func TestParseNodeUrlRejectsMalformedURL(t *testing.T) {
	for _, raw := range []string{
		"10.0.0.1:9000?name=node1&node_secret=secret",
		"http://[::1?name=node1&node_secret=secret",
		"node.example?name=node1&node_secret=secret",
		"?name=node1&node_secret=secret",
		"ftp://10.0.0.1:21?name=node1&node_secret=secret",
		"http://?name=node1&node_secret=secret",
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := parseNodeUrl(raw)
			assert.Error(t, err)
		})
	}
}
