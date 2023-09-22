package invokefunc

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eth-easl/loader/pkg/common"
	"github.com/eth-easl/loader/pkg/config"
	"github.com/eth-easl/loader/pkg/workload/proto"
	"github.com/google/uuid"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

	mc "github.com/eth-easl/loader/pkg/metric"
)

type SpeedInfo struct {
	iteration int
	runtime   int64
}

func calPriority(curIter, seconds int) int {
	return curIter / (seconds + 1)
}

func buildSingleGrpcClients(conn_list []*grpc.ClientConn, functions []*common.Function, runtimeSpec *common.RuntimeSpecification) [][]proto.ExecutorClient {
	grpcClients := make([][]proto.ExecutorClient, len(functions))
	for conn_idx, conn := range conn_list {
		for i := 0; i < common.TotalGPUs; i++ {
			grpcClients[conn_idx] = append(grpcClients[conn_idx], proto.NewExecutorClient(conn))

		}
	}
	return grpcClients
}

func prepareLocalGPUSet(upperboundReplicas int, maxGPUPerNode int) []int {
	localGPUSet := make([]int, 0)
	baseGPU := 1
	for baseGPU <= upperboundReplicas {
		localGPUSet = append(localGPUSet, baseGPU)
		baseGPU = baseGPU * 2
	}
	return localGPUSet
}

func lowerboundReplicasToDeadline(remainingService int, deadline int, GPUSet []int) int {
	for _, replicas := range GPUSet {
		jct := int(remainingService / replicas)
		if jct <= deadline {
			return replicas
		}
	}
	return -1
}

func lowerboundReplicasToDeadlineByProfileSpeedMatrix(GPUIteration int, defaultRuntime int, profileSpeedMatrix map[int]SpeedInfo, deadline int, GPUSet []int) int {
	for _, replicas := range GPUSet {
		var jct int
		if speedInfo, ok := profileSpeedMatrix[replicas]; ok {
			jct = GPUIteration / speedInfo.iteration * int(speedInfo.runtime) / replicas
		} else {
			jct = GPUIteration * defaultRuntime / replicas
		}

		if jct <= deadline {
			return min(replicas+1, GPUSet[len(GPUSet)-1])
		}
	}
	return 1 // -1
}

func INFlessInvoke(functions []*common.Function, promptFunctions []*common.Function,
	runtimeSpec *common.RuntimeSpecification, cfg *config.LoaderConfiguration, invocationID string,
	jobSchedOutputChannel chan *mc.JobSchedRequest, jobSchedInputChannel chan *mc.JobSchedReply) (bool, *mc.ExecutionRecord, *mc.JobExecutionRecord) {
	// TODO: remove this line
	functions = functions[:1]
	functionKey := invocationID
	record := &mc.ExecutionRecord{
		RequestedDuration: uint32(runtimeSpec.Runtime * 1e3),
	}

	jobRecord := &mc.JobExecutionRecord{
		InvocationID:   invocationID,
		StartTime:      make([]int64, 0),
		Replica:        make([]int, 0),
		GpuCount:       make([]int, 0),
		ComputeTime:    make([]int64, 0),
		ExecutionTime:  make([]int64, 0),
		StartIteration: make([]int, 0),
		EndIteration:   make([]int, 0),
		TotalIteration: make([]int, 0),
		BatchSize:      make([]int, 0),
	}

	jobSchedRequeset := &mc.JobSchedRequest{
		InvocationID:      invocationID,
		Replica:           uint32(0),
		BatchSize:         uint32(runtimeSpec.Stats.BatchSize),
		Iterations:        uint32(runtimeSpec.Stats.Iterations),
		Deadline:          int32(runtimeSpec.Stats.Deadline),
		RuntimeInMilliSec: uint32(runtimeSpec.Runtime),
		PrevReplica:       uint32(0),
		AvailableGPU:      common.TotalGPUs,
	}
	////////////////////////////////////
	// INVOKE FUNCTION
	////////////////////////////////////
	start := time.Now()
	record.StartTime = start.UnixMicro()
	trainingIterations := runtimeSpec.Stats.Iterations

	dialContext, cancelDialing := context.WithTimeout(context.Background(), time.Duration(cfg.GRPCConnectionTimeoutSeconds)*time.Second)
	defer cancelDialing()

	var dialOptions []grpc.DialOption
	dialOptions = append(dialOptions, grpc.WithTransportCredentials(insecure.NewCredentials()))
	dialOptions = append(dialOptions, grpc.WithBlock())
	if cfg.EnableZipkinTracing {
		// NOTE: if enabled it will exclude Istio span from the Zipkin trace
		dialOptions = append(dialOptions, grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()))
	}

	conn_list := make([]*grpc.ClientConn, len(functions))
	gpu_list := make([]int, len(functions))
	for function_idx, function := range functions {
		gpu_list[function_idx], _ = strconv.Atoi(strings.Split(function.Name, "-gpu-")[1])
		conn, err := grpc.DialContext(dialContext, function.Endpoint, dialOptions...)
		if err != nil {
			log.Debugf("Failed to establish a gRPC connection - %v\n", err)
			record.ResponseTime = time.Since(start).Milliseconds()
			record.ConnectionTimeout = true
			deleteValue(functionKey)
			return true, record, jobRecord
		}
		conn_list[function_idx] = conn
	}
	// register job state into trace scheduler
	setValue(functionKey, runtimeSpec.Stats.BatchSize/common.BszPerDevice)

	for i := 0; i < len(functions); i++ {
		defer gRPCConnectionClose(conn_list[i])
	}
	leaseTime := 900
	executionCxt, cancelExecution := context.WithTimeout(context.Background(), time.Duration(leaseTime)*time.Second)
	// add http header for scheduler
	uuid := uuid.New()
	priority := calPriority(10, 1)
	md := metadata.New(map[string]string{"GPTName": uuid.String(), "RIter": strconv.Itoa(priority)})
	executionCxt = metadata.NewOutgoingContext(executionCxt, md)

	promptTensor := make([]float32, 128*common.EmbedingDim)
	for i := range promptTensor {
		promptTensor[i] = 0
	}

	responses := make([]proto.FaasReply, common.TotalGPUs)

	// create grpc clients
	grpcClients := buildSingleGrpcClients(conn_list, functions, runtimeSpec)

	// training hyper parameters
	var totalBatchSize int
	var upperboundReplicas int
	var minReplicas int
	var specifiedReplicas int
	// send_messages := prepareMessages("Can you condense the sentence into a shorter version without losing its meaning?", 100) // communication overhead
	totalBatchSize = runtimeSpec.Stats.BatchSize
	upperboundReplicas = common.TotalGPUs
	specifiedReplicas = totalBatchSize / common.BszPerDevice
	localGPUSet := prepareRangeGPUSet(upperboundReplicas, common.GPUPerNode)

	red := "\033[31m"
	reset := "\033[0m"

	message := fmt.Sprintf("starting process %v", invocationID)
	fmt.Println(red + message + reset)

	curIter := 0
	waitBackFill := 0
	nextCreateGRPC := leaseTime
	for curIter < trainingIterations {
		// create a wait group to wait for all goroutines to finish

		var wg sync.WaitGroup
	onemore:
		{
			setSchedJobCount(invocationID)
			jobSchedRequeset.PrevReplica = uint32(minReplicas)
			jobSchedRequeset.Deadline = int32(int64(runtimeSpec.Stats.Deadline) - time.Since(start).Milliseconds())
			jobSchedRequeset.Iterations = uint32(trainingIterations - curIter)
			jobSchedOutputChannel <- jobSchedRequeset
			jobSchedReply := <-jobSchedInputChannel
			removeSchedJobCount(invocationID)
			minReplicas = -1
			for idx, jobInvocationID := range jobSchedReply.InvocationIDs {
				if jobInvocationID == invocationID {
					minReplicas = int(jobSchedReply.Replicas[idx])
				}
			}

			if minReplicas == -1 {
				record.ResponseTime = time.Since(start).Milliseconds()
				record.ConnectionTimeout = true
				cancelExecution()
				return false, record, jobRecord
			}
			setJobUsedResource(invocationID, minReplicas)
		}
		if minReplicas == 0 {
			time.Sleep(common.INFlessInterval * time.Second)
			goto onemore
		}
		curDeploymentGPUID := findIndex(localGPUSet, minReplicas)
		deploymentFuncID := min(findIndex(localGPUSet, localGPUSet[curDeploymentGPUID]), len(gpu_list)-1)
		grpcReplicas := localGPUSet[curDeploymentGPUID] / gpu_list[deploymentFuncID]
		errorOrNot := false
		iteration_per_call := trainingIterations * specifiedReplicas / localGPUSet[curDeploymentGPUID]

		doneChan := make(chan struct{})
		onceCallStart := time.Now()
		for replicaID := 0; replicaID < grpcReplicas; replicaID++ {
			// add one to the wait group
			wg.Add(1)
			// execute the function asynchronously
			go func(replicaID int) {
				defer wg.Done()
				// execute the function and store the response
				rep_start := time.Now()
				send_messages := fmt.Sprintf("invocationId %s - ReplicaID: %d", invocationID, replicaID)
				response, err := grpcClients[deploymentFuncID][replicaID].Execute(executionCxt, &proto.FaasRequest{
					Message:              send_messages,
					Batchsize:            uint32(common.BszPerDevice),
					RuntimeInMilliSec:    uint32(runtimeSpec.Runtime * iteration_per_call), // [ms]
					GpuMemoryInMebiBytes: 123,
					PromptTensor:         promptTensor,
				})
				if err != nil {
					red := "\033[32m"
					reset := "\033[0m"
					message := fmt.Sprintf("Error executing function replicaID: %d: %v\n", replicaID, err)
					fmt.Printf(red + message + reset)
					errorOrNot = errorOrNot || true
					return
				}
				red := "\033[34m"
				reset := "\033[0m"
				message := fmt.Sprintf("replicaID-Invocation %d-%s: time %f, since start %f \n", replicaID, invocationID, time.Since(rep_start).Seconds(), time.Since(start).Seconds())
				fmt.Printf(red + message + reset)
				// store the response in the slice
				responses[replicaID] = *response
			}(replicaID)
		}

		// create a goroutine to wait for all goroutines to finish
		go func() {
			wg.Wait()
			close(doneChan)
		}()
		// wait for all function invocations to finish
		<-doneChan
		removeJobUsedResource(invocationID) // TODO: key step
		if errorOrNot {
			elapsed_time := time.Since(start).Seconds()
			if elapsed_time >= float64(nextCreateGRPC) {
				nextCreateGRPC += leaseTime
				cancelExecution()
				executionCxt, cancelExecution = context.WithTimeout(context.Background(), time.Duration(leaseTime)*time.Second)
				executionCxt = metadata.NewOutgoingContext(executionCxt, md)
			}

			red := "\033[32m"
			reset := "\033[0m"

			message := fmt.Sprintf("gRPC timeout exceeded for INFless invocationID %s - %s, elapsed time %f seconds since start,  %f seconds since iteration start, trainingIterations %d, RuntimeInMilliSec %d, minReplicas %d",
				invocationID, "error", elapsed_time, time.Since(onceCallStart).Seconds(), trainingIterations, uint32(runtimeSpec.Runtime*iteration_per_call), minReplicas)
			fmt.Printf(red + message + reset)
			// log.Debugf("gRPC timeout exceeded for INFless invocationID %s - %s, elapsed time %f seconds since start,  %f seconds since iteration start, trainingIterations %d",
			// 	invocationID, "error", time.Since(start).Seconds(), time.Since(onceCallStart).Seconds(), trainingIterations)
			cmd := exec.Command("kubectl", "get", "pods")
			out, err := cmd.Output()
			if err != nil {
				fmt.Println("Error:", err)
			}
			fmt.Printf("kubectl get pods %s\n", string(out))
			cmd = exec.Command("kubectl", "get", "revisions")
			out, err = cmd.Output()
			if err != nil {
				fmt.Println("Error:", err)
			}
			fmt.Printf("kubectl get revision %s\n", string(out))

			record.ConnectionTimeout = true
			waitBackFill += 100
			time.Sleep(time.Duration(waitBackFill) * time.Millisecond)
			goto onemore
		}

		registerJobRecord(
			jobRecord,
			onceCallStart.UnixMicro(),
			int64(responses[0].DurationInMicroSec/1e3),
			time.Since(onceCallStart).Milliseconds(),
			localGPUSet[curDeploymentGPUID],
			localGPUSet[curDeploymentGPUID],
			curIter,
			curIter+iteration_per_call,
			trainingIterations,
			runtimeSpec.Stats.BatchSize,
		)
		// update lowerbound replicas to complete it before deadline
		record.ActualDuration += responses[0].DurationInMicroSec / 1e3 // ActualDuration is ms
		curIter += iteration_per_call
		break
	}

	record.Instance = extractInstanceName(responses[0].GetMessage())
	record.ResponseTime = time.Since(start).Milliseconds()
	record.Deadline = runtimeSpec.Stats.Deadline
	record.BatchSize = runtimeSpec.Stats.BatchSize
	record.Iterations = runtimeSpec.Stats.Iterations

	if strings.HasPrefix(responses[0].GetMessage(), "FAILURE - mem_alloc") {
		record.MemoryAllocationTimeout = true
	} else {
		record.ActualMemoryUsage = common.Kib2Mib(responses[0].GpuMemoryInMebiBytes)
	}

	log.Tracef("(Replied)\t %s: %s, %.2f[ms], %d[MiB]", functions[0].Name, responses[0].Message,
		float64(responses[0].DurationInMicroSec)/1e3, responses[0].GpuMemoryInMebiBytes)
	log.Tracef("(E2E Latency) %s / %s: %.2f[ms]\n", functions[0].Name, invocationID, float64(record.ResponseTime))
	log.Tracef("Length of Prompt Tensor [%d] \t Sum of Prompt Tensor [%.2f] \n", len(responses[0].PromptGradient), sum(responses[0].PromptGradient))
	cancelExecution()
	deleteValue(functionKey)
	return true, record, jobRecord
}
