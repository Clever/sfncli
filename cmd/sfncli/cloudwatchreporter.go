package main

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/cloudwatch"
	"github.com/aws/aws-sdk-go/service/cloudwatch/cloudwatchiface"
	"gopkg.in/Clever/kayvee-go.v6/logger"
)

// CloudWatchReporter reports useful metrics about the activity.
type CloudWatchReporter struct {
	cwapi       cloudwatchiface.CloudWatchAPI
	activityArn string
	idleTime    int64
}

func NewCloudWatchReporter(cwapi cloudwatchiface.CloudWatchAPI, activityArn string) *CloudWatchReporter {
	c := &CloudWatchReporter{
		cwapi:       cwapi,
		activityArn: activityArn,
	}
	return c
}

// ReportIdleTime reports time spent idle to cloudwatch as a counter.
// It stops when the context is canceled.
func (cwr *CloudWatchReporter) ReportIdleTime(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			break
		case <-ticker.C:
			val := atomic.SwapInt64(&cwr.idleTime, 0) // reset counter to zero
			log.InfoD("reportidletime", logger.M{"seconds": time.Duration(val) / time.Second})
			if _, err := cwr.cwapi.PutMetricDataWithContext(ctx, &cloudwatch.PutMetricDataInput{
				MetricData: []*cloudwatch.MetricDatum{{
					Dimensions: []*cloudwatch.Dimension{{
						Name:  aws.String("ActivityArn"),
						Value: aws.String(cwr.activityArn),
					}},
					MetricName: aws.String("ActivityIdleTimeSeconds"),
					Unit:       aws.String(cloudwatch.StandardUnitSeconds),
					Value:      aws.Float64(float64(time.Duration(val) / time.Second)),
				}},
				Namespace: aws.String("StatesCustom"),
			}); err != nil && err != context.Canceled {
				log.ErrorD("reportidletime-error", logger.M{"error": err.Error()})
				// add back value to account for failure to record it in cloudwatch
				atomic.AddInt64(&cwr.idleTime, val)
			}
		}
	}
	log.Info("reportidletime-stop")
}

// CountIdleTime records time spent idle.
func (cwr *CloudWatchReporter) CountIdleTime(idle time.Duration) {
	atomic.AddInt64(&cwr.idleTime, int64(idle))
}
