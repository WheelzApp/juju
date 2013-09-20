// Copyright 2011, 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package ec2_test

import (
	"bytes"
	"regexp"
	"sort"
	"strings"

	"launchpad.net/goamz/aws"
	amzec2 "launchpad.net/goamz/ec2"
	"launchpad.net/goamz/ec2/ec2test"
	"launchpad.net/goamz/s3"
	"launchpad.net/goamz/s3/s3test"
	gc "launchpad.net/gocheck"
	"launchpad.net/goyaml"

	"launchpad.net/juju-core/constraints"
	"launchpad.net/juju-core/environs"
	"launchpad.net/juju-core/environs/bootstrap"
	"launchpad.net/juju-core/environs/imagemetadata"
	"launchpad.net/juju-core/environs/jujutest"
	"launchpad.net/juju-core/environs/simplestreams"
	envtesting "launchpad.net/juju-core/environs/testing"
	"launchpad.net/juju-core/environs/tools"
	"launchpad.net/juju-core/instance"
	"launchpad.net/juju-core/provider"
	"launchpad.net/juju-core/provider/ec2"
	"launchpad.net/juju-core/testing"
	jc "launchpad.net/juju-core/testing/checkers"
	"launchpad.net/juju-core/utils"
)

type ProviderSuite struct{}

var _ = gc.Suite(&ProviderSuite{})

func (s *ProviderSuite) TestMetadata(c *gc.C) {
	metadataContent := map[string]string{
		"/2011-01-01/meta-data/public-hostname": "public.dummy.address.invalid",
		"/2011-01-01/meta-data/local-hostname":  "private.dummy.address.invalid",
	}
	ec2.UseTestMetadata(metadataContent)
	defer ec2.UseTestMetadata(nil)

	p, err := environs.Provider("ec2")
	c.Assert(err, gc.IsNil)

	addr, err := p.PublicAddress()
	c.Assert(err, gc.IsNil)
	c.Assert(addr, gc.Equals, "public.dummy.address.invalid")

	addr, err = p.PrivateAddress()
	c.Assert(err, gc.IsNil)
	c.Assert(addr, gc.Equals, "private.dummy.address.invalid")
}

var localConfigAttrs = testing.FakeConfig().Merge(testing.Attrs{
	"name":                 "sample",
	"type":                 "ec2",
	"region":               "test",
	"control-bucket":       "test-bucket",
	"public-bucket":        "public-tools",
	"public-bucket-region": "test",
	"access-key":           "x",
	"secret-key":           "x",
})

func registerLocalTests() {
	// N.B. Make sure the region we use here
	// has entries in the images/query txt files.
	aws.Regions["test"] = aws.Region{
		Name: "test",
	}

	gc.Suite(&localServerSuite{})
	gc.Suite(&localLiveSuite{})
	gc.Suite(&localNonUSEastSuite{})
}

// localLiveSuite runs tests from LiveTests using a fake
// EC2 server that runs within the test process itself.
type localLiveSuite struct {
	LiveTests
	srv                localServer
	restoreEC2Patching func()
}

func (t *localLiveSuite) SetUpSuite(c *gc.C) {
	t.TestConfig = localConfigAttrs
	t.restoreEC2Patching = patchEC2ForTesting()
	t.srv.startServer(c)
	t.LiveTests.SetUpSuite(c)
}

func (t *localLiveSuite) TearDownSuite(c *gc.C) {
	t.LiveTests.TearDownSuite(c)
	t.srv.stopServer(c)
	t.restoreEC2Patching()
}

// localServer represents a fake EC2 server running within
// the test process itself.
type localServer struct {
	ec2srv *ec2test.Server
	s3srv  *s3test.Server
	config *s3test.Config
}

func (srv *localServer) startServer(c *gc.C) {
	var err error
	srv.ec2srv, err = ec2test.NewServer()
	if err != nil {
		c.Fatalf("cannot start ec2 test server: %v", err)
	}
	srv.s3srv, err = s3test.NewServer(srv.config)
	if err != nil {
		c.Fatalf("cannot start s3 test server: %v", err)
	}
	aws.Regions["test"] = aws.Region{
		Name:                 "test",
		EC2Endpoint:          srv.ec2srv.URL(),
		S3Endpoint:           srv.s3srv.URL(),
		S3LocationConstraint: true,
	}
	s3inst := s3.New(aws.Auth{}, aws.Regions["test"])
	writeablePublicStorage := ec2.BucketStorage(s3inst.Bucket("public-tools"))
	envtesting.UploadFakeTools(c, writeablePublicStorage)
	srv.addSpice(c)
}

// addSpice adds some "spice" to the local server
// by adding state that may cause tests to fail.
func (srv *localServer) addSpice(c *gc.C) {
	states := []amzec2.InstanceState{
		ec2test.ShuttingDown,
		ec2test.Terminated,
		ec2test.Stopped,
	}
	for _, state := range states {
		srv.ec2srv.NewInstances(1, "m1.small", "ami-a7f539ce", state, nil)
	}
}

func (srv *localServer) stopServer(c *gc.C) {
	srv.ec2srv.Quit()
	srv.s3srv.Quit()
	// Clear out the region because the server address is
	// no longer valid.
	delete(aws.Regions, "test")
}

// localServerSuite contains tests that run against a fake EC2 server
// running within the test process itself.  These tests can test things that
// would be unreasonably slow or expensive to test on a live Amazon server.
// It starts a new local ec2test server for each test.  The server is
// accessed by using the "test" region, which is changed to point to the
// network address of the local server.
type localServerSuite struct {
	jujutest.Tests
	srv                localServer
	restoreEC2Patching func()
}

func (t *localServerSuite) SetUpSuite(c *gc.C) {
	t.TestConfig = localConfigAttrs
	t.restoreEC2Patching = patchEC2ForTesting()
	t.Tests.SetUpSuite(c)
}

func (t *localServerSuite) TearDownSuite(c *gc.C) {
	t.Tests.TearDownSuite(c)
	t.restoreEC2Patching()
}

func (t *localServerSuite) SetUpTest(c *gc.C) {
	t.srv.startServer(c)
	t.Tests.SetUpTest(c)
}

func (t *localServerSuite) TearDownTest(c *gc.C) {
	t.Tests.TearDownTest(c)
	t.srv.stopServer(c)
}

func (t *localServerSuite) TestBootstrapInstanceUserDataAndState(c *gc.C) {
	envtesting.UploadFakeTools(c, t.Env.Storage())
	err := bootstrap.Bootstrap(t.Env, constraints.Value{})
	c.Assert(err, gc.IsNil)

	// check that the state holds the id of the bootstrap machine.
	bootstrapState, err := provider.LoadState(t.Env.Storage())
	c.Assert(err, gc.IsNil)
	c.Assert(bootstrapState.StateInstances, gc.HasLen, 1)

	expectedHardware := instance.MustParseHardware("arch=amd64 cpu-cores=1 cpu-power=100 mem=1740M root-disk=8192M")
	insts, err := t.Env.AllInstances()
	c.Assert(err, gc.IsNil)
	c.Assert(insts, gc.HasLen, 1)
	c.Check(insts[0].Id(), gc.Equals, bootstrapState.StateInstances[0])
	c.Check(expectedHardware, gc.DeepEquals, bootstrapState.Characteristics[0])

	info, apiInfo, err := t.Env.StateInfo()
	c.Assert(err, gc.IsNil)
	c.Assert(info, gc.NotNil)

	// check that the user data is configured to start zookeeper
	// and the machine and provisioning agents.
	inst := t.srv.ec2srv.Instance(string(insts[0].Id()))
	c.Assert(inst, gc.NotNil)
	bootstrapDNS, err := insts[0].DNSName()
	c.Assert(err, gc.IsNil)
	c.Assert(bootstrapDNS, gc.Not(gc.Equals), "")

	userData, err := utils.Gunzip(inst.UserData)
	c.Assert(err, gc.IsNil)
	c.Logf("first instance: UserData: %q", userData)
	var x map[interface{}]interface{}
	err = goyaml.Unmarshal(userData, &x)
	c.Assert(err, gc.IsNil)
	CheckPackage(c, x, "git", true)
	CheckScripts(c, x, "jujud bootstrap-state", true)
	// TODO check for provisioning agent
	// TODO check for machine agent

	// check that a new instance will be started without
	// zookeeper, with a machine agent, and without a
	// provisioning agent.
	series := t.Env.Config().DefaultSeries()
	info.Tag = "machine-1"
	info.Password = "password"
	apiInfo.Tag = "machine-1"
	inst1, hc, err := provider.StartInstance(t.Env, "1", "fake_nonce", series, constraints.Value{}, info, apiInfo)
	c.Assert(err, gc.IsNil)
	c.Check(*hc.Arch, gc.Equals, "amd64")
	c.Check(*hc.Mem, gc.Equals, uint64(1740))
	c.Check(*hc.CpuCores, gc.Equals, uint64(1))
	c.Assert(*hc.CpuPower, gc.Equals, uint64(100))
	inst = t.srv.ec2srv.Instance(string(inst1.Id()))
	c.Assert(inst, gc.NotNil)
	userData, err = utils.Gunzip(inst.UserData)
	c.Assert(err, gc.IsNil)
	c.Logf("second instance: UserData: %q", userData)
	x = nil
	err = goyaml.Unmarshal(userData, &x)
	c.Assert(err, gc.IsNil)
	CheckPackage(c, x, "zookeeperd", false)
	// TODO check for provisioning agent
	// TODO check for machine agent

	err = t.Env.Destroy(append(insts, inst1))
	c.Assert(err, gc.IsNil)

	_, err = provider.LoadState(t.Env.Storage())
	c.Assert(err, gc.NotNil)
}

func (t *localServerSuite) TestInstanceStatus(c *gc.C) {
	err := bootstrap.Bootstrap(t.Env, constraints.Value{})
	c.Assert(err, gc.IsNil)
	series := t.Env.Config().DefaultSeries()
	info, apiInfo, err := t.Env.StateInfo()
	c.Assert(err, gc.IsNil)
	c.Assert(info, gc.NotNil)
	info.Tag = "machine-1"
	info.Password = "password"
	apiInfo.Tag = "machine-1"
	t.srv.ec2srv.SetInitialInstanceState(ec2test.Terminated)
	inst, _, err := provider.StartInstance(t.Env, "1", "fake_nonce", series, constraints.Value{}, info, apiInfo)
	c.Assert(err, gc.IsNil)
	c.Assert(inst.Status(), gc.Equals, "terminated")
}

func (t *localServerSuite) TestStartInstanceHardwareCharacteristics(c *gc.C) {
	err := bootstrap.Bootstrap(t.Env, constraints.Value{})
	c.Assert(err, gc.IsNil)
	series := t.Env.Config().DefaultSeries()
	info, apiInfo, err := t.Env.StateInfo()
	c.Assert(err, gc.IsNil)
	c.Assert(info, gc.NotNil)
	info.Tag = "machine-1"
	info.Password = "password"
	apiInfo.Tag = "machine-1"
	_, hc, err := provider.StartInstance(t.Env, "1", "fake_nonce", series, constraints.MustParse("mem=1024"), info, apiInfo)
	c.Assert(err, gc.IsNil)
	c.Check(*hc.Arch, gc.Equals, "amd64")
	c.Check(*hc.Mem, gc.Equals, uint64(1740))
	c.Check(*hc.CpuCores, gc.Equals, uint64(1))
	c.Assert(*hc.CpuPower, gc.Equals, uint64(100))
}

func (t *localServerSuite) TestAddresses(c *gc.C) {
	err := bootstrap.Bootstrap(t.Env, constraints.Value{})
	c.Assert(err, gc.IsNil)
	series := t.Env.Config().DefaultSeries()
	info, apiInfo, err := t.Env.StateInfo()
	c.Assert(err, gc.IsNil)
	c.Assert(info, gc.NotNil)
	info.Tag = "machine-1"
	info.Password = "password"
	apiInfo.Tag = "machine-1"
	inst, _, err := provider.StartInstance(t.Env, "1", "fake_nonce", series, constraints.Value{}, info, apiInfo)
	c.Assert(err, gc.IsNil)
	addrs, err := inst.Addresses()
	c.Assert(err, gc.IsNil)
	// Expected values use Address type but really contain a regexp for
	// the value rather than a valid ip or hostname.
	expected := []instance.Address{{
		Value:        "*.testing.invalid",
		Type:         instance.HostName,
		NetworkScope: instance.NetworkPublic,
	}, {
		Value:        "*.internal.invalid",
		Type:         instance.HostName,
		NetworkScope: instance.NetworkCloudLocal,
	}, {
		Value:        "8.0.0.*",
		Type:         instance.Ipv4Address,
		NetworkScope: instance.NetworkPublic,
	}, {
		Value:        "127.0.0.*",
		Type:         instance.Ipv4Address,
		NetworkScope: instance.NetworkCloudLocal,
	}}
	c.Assert(addrs, gc.HasLen, len(expected))
	for i, addr := range addrs {
		c.Check(addr.Value, gc.Matches, expected[i].Value)
		c.Check(addr.Type, gc.Equals, expected[i].Type)
		c.Check(addr.NetworkScope, gc.Equals, expected[i].NetworkScope)
	}
}

func (t *localServerSuite) TestValidateImageMetadata(c *gc.C) {
	params, err := t.Env.(simplestreams.MetadataValidator).MetadataLookupParams("test")
	c.Assert(err, gc.IsNil)
	params.Series = "precise"
	params.Endpoint = "https://ec2.endpoint.com"
	params.Sources, err = imagemetadata.GetMetadataSources(t.Env)
	c.Assert(err, gc.IsNil)
	image_ids, err := imagemetadata.ValidateImageMetadata(params)
	c.Assert(err, gc.IsNil)
	sort.Strings(image_ids)
	c.Assert(image_ids, gc.DeepEquals, []string{"ami-00000033", "ami-00000034", "ami-00000035"})
}

func (t *localServerSuite) TestGetImageMetadataSources(c *gc.C) {
	sources, err := imagemetadata.GetMetadataSources(t.Env)
	c.Assert(err, gc.IsNil)
	c.Assert(len(sources), gc.Equals, 2)
	var urls = make([]string, len(sources))
	for i, source := range sources {
		url, err := source.URL("")
		c.Assert(err, gc.IsNil)
		urls[i] = url
	}
	// The control bucket URL contains the bucket name.
	c.Check(strings.Contains(urls[0], ec2.ControlBucketName(t.Env)), jc.IsTrue)
	c.Assert(urls[1], gc.Equals, imagemetadata.DefaultBaseURL+"/")
}

func (t *localServerSuite) TestGetToolsMetadataSources(c *gc.C) {
	sources, err := tools.GetMetadataSources(t.Env)
	c.Assert(err, gc.IsNil)
	c.Assert(len(sources), gc.Equals, 1)
	url, err := sources[0].URL("")
	// The control bucket URL contains the bucket name.
	c.Assert(strings.Contains(url, ec2.ControlBucketName(t.Env)+"/tools"), jc.IsTrue)
}

// localNonUSEastSuite is similar to localServerSuite but the S3 mock server
// behaves as if it is not in the us-east region.
type localNonUSEastSuite struct {
	testbase.LoggingSuite
	restoreEC2Patching func()
	srv                localServer
	env                environs.Environ
}

func (t *localNonUSEastSuite) SetUpSuite(c *gc.C) {
	t.LoggingSuite.SetUpSuite(c)
	t.restoreEC2Patching = patchEC2ForTesting()
}

func (t *localNonUSEastSuite) TearDownSuite(c *gc.C) {
	t.restoreEC2Patching()
	t.LoggingSuite.TearDownSuite(c)
}

func (t *localNonUSEastSuite) SetUpTest(c *gc.C) {
	t.LoggingSuite.SetUpTest(c)
	t.srv.config = &s3test.Config{
		Send409Conflict: true,
	}
	t.srv.startServer(c)

	env, err := environs.NewFromAttrs(localConfigAttrs)
	c.Assert(err, gc.IsNil)
	t.env = env
}

func (t *localNonUSEastSuite) TearDownTest(c *gc.C) {
	t.srv.stopServer(c)
	t.LoggingSuite.TearDownTest(c)
}

func (t *localNonUSEastSuite) TestPutBucket(c *gc.C) {
	p := ec2.WritablePublicStorage(t.env).(ec2.Storage)
	for i := 0; i < 5; i++ {
		p.ResetMadeBucket()
		var buf bytes.Buffer
		err := p.Put("test-file", &buf, 0)
		c.Assert(err, gc.IsNil)
	}
}

func patchEC2ForTesting() func() {
	ec2.UseTestImageData(ec2.TestImagesData)
	ec2.UseTestInstanceTypeData(ec2.TestInstanceTypeCosts)
	ec2.UseTestRegionData(ec2.TestRegions)
	restoreTimeouts := envtesting.PatchAttemptStrategies(ec2.ShortAttempt, ec2.StorageAttempt)
	return func() {
		restoreTimeouts()
		ec2.UseTestImageData(nil)
		ec2.UseTestInstanceTypeData(nil)
		ec2.UseTestRegionData(nil)
	}
}

// If match is true, CheckScripts checks that at least one script started
// by the cloudinit data matches the given regexp pattern, otherwise it
// checks that no script matches.  It's exported so it can be used by tests
// defined in ec2_test.
func CheckScripts(c *gc.C, x map[interface{}]interface{}, pattern string, match bool) {
	scripts0 := x["runcmd"]
	if scripts0 == nil {
		c.Errorf("cloudinit has no entry for runcmd")
		return
	}
	scripts := scripts0.([]interface{})
	re := regexp.MustCompile(pattern)
	found := false
	for _, s0 := range scripts {
		s := s0.(string)
		if re.MatchString(s) {
			found = true
		}
	}
	switch {
	case match && !found:
		c.Errorf("script %q not found in %q", pattern, scripts)
	case !match && found:
		c.Errorf("script %q found but not expected in %q", pattern, scripts)
	}
}

// CheckPackage checks that the cloudinit will or won't install the given
// package, depending on the value of match.  It's exported so it can be
// used by tests defined outside the ec2 package.
func CheckPackage(c *gc.C, x map[interface{}]interface{}, pkg string, match bool) {
	pkgs0 := x["packages"]
	if pkgs0 == nil {
		if match {
			c.Errorf("cloudinit has no entry for packages")
		}
		return
	}

	pkgs := pkgs0.([]interface{})

	found := false
	for _, p0 := range pkgs {
		p := p0.(string)
		if p == pkg {
			found = true
		}
	}
	switch {
	case match && !found:
		c.Errorf("package %q not found in %v", pkg, pkgs)
	case !match && found:
		c.Errorf("%q found but not expected in %v", pkg, pkgs)
	}
}
