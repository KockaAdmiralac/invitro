/*
 * MIT License
 *
 * Copyright (c) 2023 EASL and the vHive community
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package driver

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/vhive-serverless/loader/pkg/common"

	log "github.com/sirupsen/logrus"
)

const (
	bareMetalLbGateway = "10.200.3.4.sslip.io" // Address of the bare-metal load balancer.
	namespace          = "default"
)

var (
	urlRegex = regexp.MustCompile("at URL:\nhttp://([^\n]+)")
)

func DeployFunctions(functions []*common.Function, yamlPath string, isPartiallyPanic bool, endpointPort int, autoscalingMetric string) {
	for i := 0; i < len(functions); i++ {
		deployKnative(functions[i], yamlPath, isPartiallyPanic, endpointPort, autoscalingMetric)
	}
}

func DeployDirigent(functions []*common.Function) {
	for i := 0; i < len(functions); i++ {
		deployDirigent(functions[i])
	}
}

func deployDirigent(function *common.Function) {
	metadata := function.DirigentMetadata

	if metadata == nil {
		log.Fatalf("No Dirigent metadata for function %s", function.Name)
	}

	payload := url.Values{
		"name":                {function.Name},
		"image":               {metadata.Image},
		"port_forwarding":     {strconv.Itoa(metadata.Port), metadata.Protocol},
		"scaling_upper_bound": {strconv.Itoa(metadata.ScalingUpperBound)},
		"scaling_lower_bound": {strconv.Itoa(metadata.ScalingLowerBound)},
	}

	log.Debug(payload)

	resp, err := http.PostForm("http://localhost:9091/registerService", payload)
	if err != nil {
		log.Fatal("Failed to register a service with the control plane - ", err.Error())
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatal("Failed to read response body.")
	}

	endpoints := strings.Split(string(body), ";")
	if len(endpoints) == 0 {
		log.Fatal("Function registration returned no data plane(s).")
	}
	function.Endpoint = endpoints[rand.Intn(len(endpoints))]
}

func deployPolicyExperiment(function *common.Function) {

}

func deployKnative(function *common.Function, yamlPath string, isPartiallyPanic bool, endpointPort int,
	autoscalingMetric string) bool {
	panicWindow := "\"10.0\""
	panicThreshold := "\"200.0\""
	if isPartiallyPanic {
		panicWindow = "\"100.0\""
		panicThreshold = "\"1000.0\""
	}
	autoscalingTarget := 100 // default for concurrency
	if autoscalingMetric == "rps" {
		autoscalingTarget = int(math.Round(1000.0 / function.RuntimeStats.Average))
		// for rps mode use the average runtime in milliseconds to determine how many requests a pod can process per
		// second, then round to an integer as that is what the knative config expects
	}

	val := 0
	for _, char := range function.Name {
		val += int(char)
	}
	nofImages := 6
	mod := val % nofImages

	var sed *exec.Cmd
	switch mod {
	case 0:
		sed = exec.Command("sed", "'s/image: .*latest/image: lfavento\\/trace-5m:latest/'", yamlPath)
	case 1:
		sed = exec.Command("sed", "'s/image: .*latest/image: lfavento\\/trace-10m:latest/'", yamlPath)
	case 2:
		sed = exec.Command("sed", "'s/image: .*latest/image: lfavento\\/trace-20m:latest/'", yamlPath)
	case 3:
		sed = exec.Command("sed", "'s/image: .*latest/image: lfavento\\/trace-30m:latest/'", yamlPath)
	case 4:
		sed = exec.Command("sed", "'s/image: .*latest/image: lfavento\\/trace-40m:latest/'", yamlPath)
	case 5:
		sed = exec.Command("sed", "'s/image: .*latest/image: lfavento\\/trace-50m:latest/'", yamlPath)
	}
	err := sed.Run()
	if err != nil {
		log.Warnf("Failed to alter %s content.\n", yamlPath)
		return false
	}

	cmd := exec.Command(
		"bash",
		"./pkg/driver/deploy.sh",
		yamlPath,
		function.Name,

		strconv.Itoa(function.CPURequestsMilli)+"m",
		strconv.Itoa(function.CPULimitsMilli)+"m",
		strconv.Itoa(function.MemoryRequestsMiB)+"Mi",
		strconv.Itoa(function.InitialScale),

		panicWindow,
		panicThreshold,

		"\""+autoscalingMetric+"\"",
		"\""+strconv.Itoa(autoscalingTarget)+"\"",
	)

	stdoutStderr, err := cmd.CombinedOutput()
	log.Debug("CMD response: ", string(stdoutStderr))

	if err != nil {
		// TODO: there should be a toggle to turn off deployment because if this is fatal then we cannot test the thing locally
		log.Warnf("Failed to deploy function %s: %v\n%s\n", function.Name, err, stdoutStderr)

		return false
	}

	if endpoint := urlRegex.FindStringSubmatch(string(stdoutStderr))[1]; endpoint != function.Endpoint {
		// TODO: check when this situation happens
		log.Debugf("Update function endpoint to %s\n", endpoint)
		function.Endpoint = endpoint
	} else {
		function.Endpoint = fmt.Sprintf("%s.%s.%s", function.Name, namespace, bareMetalLbGateway)
	}

	// adding port to the endpoint
	function.Endpoint = fmt.Sprintf("%s:%d", function.Endpoint, endpointPort)

	log.Debugf("Deployed function on %s\n", function.Endpoint)
	return true
}

func CleanKnative() {
	cmd := exec.Command("kn", "service", "delete", "--all")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		log.Debugf("Unable to delete Knative services - %s", err)
	}
}
