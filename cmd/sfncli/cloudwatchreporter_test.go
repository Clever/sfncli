package main

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/Clever/sfncli/gen-go/mockcloudwatch"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/cloudwatch"
	"github.com/golang/mock/gomock"
)

const mockActivityArn = "mockActivityArn"

func TestCloudWatchReporterReportsActiveZero(t *testing.T) {
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	controller := gomock.NewController(t)
	defer controller.Finish()
	mockCW := mockcloudwatch.NewMockCloudWatchAPI(controller)
	cwr := NewCloudWatchReporter(mockCW, mockActivityArn)
	go cwr.ReportActivePercent(testCtx, 100*time.Millisecond)
	mockCW.EXPECT().PutMetricData(&cloudwatch.PutMetricDataInput{
		MetricData: []*cloudwatch.MetricDatum{{
			Dimensions: []*cloudwatch.Dimension{{
				Name:  aws.String("ActivityArn"),
				Value: aws.String(mockActivityArn),
			}},
			MetricName: aws.String(metricNameActivityActivePercent),
			Unit:       aws.String(cloudwatch.StandardUnitPercent),
			Value:      aws.Float64(0.0),
		}},
		Namespace: aws.String(namespaceStatesCustom),
	})
	time.Sleep(100*time.Millisecond + 10*time.Millisecond)
}

func TestCloudWatchReporterReportsActiveFiftyPercent(t *testing.T) {
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	controller := gomock.NewController(t)
	defer controller.Finish()
	mockCW := mockcloudwatch.NewMockCloudWatchAPI(controller)
	mockCW.EXPECT().PutMetricData(fuzzy(&cloudwatch.PutMetricDataInput{
		MetricData: []*cloudwatch.MetricDatum{{
			Dimensions: []*cloudwatch.Dimension{{
				Name:  aws.String("ActivityArn"),
				Value: aws.String(mockActivityArn),
			}},
			MetricName: aws.String(metricNameActivityActivePercent),
			Unit:       aws.String(cloudwatch.StandardUnitPercent),
			Value:      aws.Float64(50.0),
		}},
		Namespace: aws.String(namespaceStatesCustom),
	})).Times(2)
	cwr := NewCloudWatchReporter(mockCW, mockActivityArn)
	go cwr.ReportActivePercent(testCtx, 1*time.Second)
	go func() {
		// active for 500 ms in first second and second second
		time.Sleep(500 * time.Millisecond)
		cwr.SetActiveState(true)
		time.Sleep(1 * time.Second)
		cwr.SetActiveState(false)
	}()
	// check after 2 seconds, should be 50% active on both intervals
	time.Sleep(2*time.Second + 100*time.Millisecond)
}

func TestCloudWatchReporterReportsActiveHundredPercent(t *testing.T) {
	testCtx, testCtxCancel := context.WithCancel(context.Background())
	defer testCtxCancel()
	controller := gomock.NewController(t)
	defer controller.Finish()
	mockCW := mockcloudwatch.NewMockCloudWatchAPI(controller)
	mockCW.EXPECT().PutMetricData(fuzzy(&cloudwatch.PutMetricDataInput{
		MetricData: []*cloudwatch.MetricDatum{{
			Dimensions: []*cloudwatch.Dimension{{
				Name:  aws.String("ActivityArn"),
				Value: aws.String(mockActivityArn),
			}},
			MetricName: aws.String(metricNameActivityActivePercent),
			Unit:       aws.String(cloudwatch.StandardUnitPercent),
			Value:      aws.Float64(100.0),
		}},
		Namespace: aws.String(namespaceStatesCustom),
	})).Times(2)
	cwr := NewCloudWatchReporter(mockCW, mockActivityArn)
	go cwr.ReportActivePercent(testCtx, 1*time.Second)
	go cwr.ActiveUntilContextDone(testCtx)
	time.Sleep(2*time.Second + 100*time.Millisecond)
}

// fuzzyMatcher is a gomock.Matcher that does a fuzzy match on cloudwatch putmetricdata values
type fuzzyMatcher struct {
	expected *cloudwatch.PutMetricDataInput
}

func fuzzy(expected *cloudwatch.PutMetricDataInput) gomock.Matcher {
	return fuzzyMatcher{expected}
}

func (f fuzzyMatcher) Matches(x interface{}) bool {
	got, ok := x.(*cloudwatch.PutMetricDataInput)
	if !ok {
		return false
	}
	epsilon := 2.00 // within 2 percent is fine
	if len(f.expected.MetricData) != len(got.MetricData) {
		return reflect.DeepEqual(f.expected, got)
	}
	for i, md := range f.expected.MetricData {
		if math.Abs(aws.Float64Value(md.Value)-aws.Float64Value(got.MetricData[i].Value)) > epsilon {
			return false
		}
		// so that deepequal succeeds, make values match exactly if they're within epsilon
		f.expected.MetricData[i].Value = got.MetricData[i].Value
	}
	return reflect.DeepEqual(f.expected, x)
}

func (f fuzzyMatcher) String() string {
	return fmt.Sprintf("is equal to %v", f.expected)
}
