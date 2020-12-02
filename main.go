package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"golang.org/x/crypto/ssh"
)

var (
	applicationStartCommand string
	applicationStopCommand  string
	defaultRegion           string
	osUser                  string
	//secretName              string
)

// AWS Key Pair
const pem = ``

type SNSMessage struct {
	AlarmName        string `json:"AlarmName"`
	AlarmDescription string `json:"AlarmDescription"`
	AWSAccountID     string `json:"AWSAccountId"`
	NewStateValue    string `json:"NewStateValue"`
	NewStateReason   string `json:"NewStateReason"`
	StateChangeTime  string `json:"StateChangeTime"`
	Region           string `json:"Region"`
	AlarmArn         string `json:"AlarmArn"`
	OldStateValue    string `json:"OldStateValue"`
	Trigger          struct {
		MetricName    string      `json:"MetricName"`
		Namespace     string      `json:"Namespace"`
		StatisticType string      `json:"StatisticType"`
		Statistic     string      `json:"Statistic"`
		Unit          interface{} `json:"Unit"`
		Dimensions    []struct {
			Value string `json:"value"`
			Name  string `json:"name"`
		} `json:"Dimensions"`
		Period                           int    `json:"Period"`
		EvaluationPeriods                int    `json:"EvaluationPeriods"`
		ComparisonOperator               string `json:"ComparisonOperator"`
		Threshold                        int    `json:"Threshold"`
		TreatMissingData                 string `json:"TreatMissingData"`
		EvaluateLowSampleCountPercentile string `json:"EvaluateLowSampleCountPercentile"`
	} `json:"Trigger"`
}

func handler(ctx context.Context, event events.SNSEvent) {
	var snsMessage SNSMessage
	if len(event.Records) > 0 {
		for _, record := range event.Records {
			err := json.Unmarshal([]byte(record.SNS.Message), &snsMessage)
			if err != nil {
				log.Print(err)
			}
			if len(snsMessage.Trigger.Dimensions) > 0 {
				for _, resource := range snsMessage.Trigger.Dimensions {
					if resource.Name == "TargetGroup" {
						targetGroupArn := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:197542431507:%s", defaultRegion, resource.Value)
						svc := elbv2.New(session.New(&aws.Config{
							Region: aws.String(defaultRegion),
						}))
						input := &elbv2.DescribeTargetHealthInput{
							TargetGroupArn: aws.String(targetGroupArn),
						}
						resp, err := svc.DescribeTargetHealth(input)
						if err != nil {
							if aerr, ok := err.(awserr.Error); ok {
								switch aerr.Code() {
								case elbv2.ErrCodeInvalidTargetException:
									log.Println(elbv2.ErrCodeInvalidTargetException, aerr.Error())
								case elbv2.ErrCodeTargetGroupNotFoundException:
									log.Println(elbv2.ErrCodeTargetGroupNotFoundException, aerr.Error())
								case elbv2.ErrCodeHealthUnavailableException:
									log.Println(elbv2.ErrCodeHealthUnavailableException, aerr.Error())
								default:
									log.Println(aerr.Error())
								}
							} else {
								log.Println(err.Error())
							}
						}
						if len(resp.TargetHealthDescriptions) > 0 {
							for _, targetHealthDesc := range resp.TargetHealthDescriptions {
								if *targetHealthDesc.TargetHealth.State == "unhealthy" {
									unhealthyHostIp := getInstanceIpByInstanceId(*targetHealthDesc.Target.Id)
									log.Printf("Unhealthy target is %s, the IP is %s", *targetHealthDesc.Target.Id, unhealthyHostIp)
									signer, err := ssh.ParsePrivateKey([]byte(pem))
									if err != nil {
										log.Fatalf("unable to parse private key: %v", err)
									}
									config := &ssh.ClientConfig{
										User: osUser,
										Auth: []ssh.AuthMethod{
											// Use the PublicKeys method for remote authentication.
											ssh.PublicKeys(signer),
										},
										HostKeyCallback: ssh.InsecureIgnoreHostKey(),
									}
									stopResult := executeCmd(applicationStopCommand, unhealthyHostIp, config)
									log.Printf("stop command is result: %s", stopResult)
									startResult := executeCmd(applicationStartCommand, unhealthyHostIp, config)
									log.Printf("start command is result: %s", startResult)
								}
							}
						}
					}
					log.Printf("Resource name is %s, value is %s\n", resource.Name, resource.Value)
				}
			}
			log.Printf("Region is %s\n", snsMessage.Region)
		}
	}
}

func executeCmd(cmd, hostname string, config *ssh.ClientConfig) string {
	conn, _ := ssh.Dial("tcp", hostname+":22", config)
	session, _ := conn.NewSession()
	defer session.Close()
	var stdoutBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Run(cmd)
	return hostname + ": " + stdoutBuf.String()
}

func getInstanceIpByInstanceId(instanceId string) string {
	svc := ec2.New(session.New(&aws.Config{
		Region: aws.String(defaultRegion),
	}))
	input := &ec2.DescribeInstancesInput{
		InstanceIds: []*string{aws.String(instanceId)},
	}
	result, err := svc.DescribeInstances(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			default:
				fmt.Println(aerr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			fmt.Println(err.Error())
		}
		return ""
	}
	return *result.Reservations[0].Instances[0].PrivateIpAddress
}

func main() {
	applicationStartCommand = os.Getenv("RUNTIME_START_CMD")
	applicationStopCommand = os.Getenv("RUNTIME_STOP_CMD")
	defaultRegion = os.Getenv("DEFAULT_REGION")
	osUser = os.Getenv("OS_USER")
	//secretName = os.Getenv("PEM_SECRET_NAME")
	lambda.Start(handler)
}
