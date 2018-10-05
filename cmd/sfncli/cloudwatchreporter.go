package main

import (
	"context"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/cloudwatch"
	"github.com/aws/aws-sdk-go/service/cloudwatch/cloudwatchiface"
	"gopkg.in/Clever/kayvee-go.v6/logger"
)

const metricNameActivityActivePercent = "ActivityActivePercent"
const namespaceStatesCustom = "StatesCustom"

// CloudWatchReporter reports useful metrics about the activity.
type CloudWatchReporter struct {
	cwapi       cloudwatchiface.CloudWatchAPI
	activityArn string

	// state to keep track of active percent
	// the mutex is here to control access by two goroutines, e.g.
	// 1. the goroutine for `ReportActivePercent`
	// 2. the goroutine that calls ActiveUntilContextDone
	mu                    sync.Mutex
	activeState           bool
	activeTime            time.Duration
	lastReportingTime     time.Time
	lastActiveStateChange time.Time
}

func NewCloudWatchReporter(cwapi cloudwatchiface.CloudWatchAPI, activityArn string) *CloudWatchReporter {
	now := time.Now()
	c := &CloudWatchReporter{
		cwapi:       cwapi,
		activityArn: activityArn,

		activeState:           false,
		activeTime:            time.Duration(0),
		lastReportingTime:     now,
		lastActiveStateChange: now,
	}
	return c
}

// ReportActivePercent sets up a loop that will report active percent to cloudwatch on an interval.
// It stops when the context is canceled.
func (c *CloudWatchReporter) ReportActivePercent(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			break
		case <-ticker.C:
			c.report()
		}
	}
}

// ActiveUntilContextDone sets active state to true, and sets it false when the context is done.
func (c *CloudWatchReporter) ActiveUntilContextDone(ctx context.Context) {
	c.SetActiveState(true)
	<-ctx.Done()
	c.SetActiveState(false)
}

// SetActiveState sets whether the activity is currently working on a task or not.
func (c *CloudWatchReporter) SetActiveState(active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if active == c.activeState {
		return
	}
	now := time.Now()
	// going from active to inactive, so record incremental active time
	if c.activeState {
		c.activeTime += now.Sub(maxTime(c.lastReportingTime, c.lastActiveStateChange))
	}
	c.activeState = active
	c.lastActiveStateChange = now
}

// maxTime returns the maximum between two times
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// report computes and sends the active time metric to cloudwatch, resetting state related to tracking active time.
func (c *CloudWatchReporter) report() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	// going from active to inactive, so record incremental active time
	if c.activeState {
		c.activeTime += now.Sub(maxTime(c.lastReportingTime, c.lastActiveStateChange))
	}
	activePercent := 100.0 * float64(c.activeTime) / float64(now.Sub(c.lastReportingTime))
	c.lastReportingTime = now
	c.activeTime = time.Duration(0)
	// fire and forget the metric
	go c.putMetricData(activePercent)
}

func (c *CloudWatchReporter) putMetricData(activePercent float64) {
	log.TraceD("put-metric-data", logger.M{"activity-arn": c.activityArn, "metric-name": metricNameActivityActivePercent, "value": activePercent})
	if _, err := c.cwapi.PutMetricData(&cloudwatch.PutMetricDataInput{
		MetricData: []*cloudwatch.MetricDatum{{
			Dimensions: []*cloudwatch.Dimension{{
				Name:  aws.String("ActivityArn"),
				Value: aws.String(c.activityArn),
			}},
			MetricName: aws.String(metricNameActivityActivePercent),
			Unit:       aws.String(cloudwatch.StandardUnitPercent),
			Value:      aws.Float64(activePercent),
		}},
		Namespace: aws.String(namespaceStatesCustom),
	}); err != nil {
		log.ErrorD("put-metric-data", logger.M{"error": err.Error()})
	}
}
