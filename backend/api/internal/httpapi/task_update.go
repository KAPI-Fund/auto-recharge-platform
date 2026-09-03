package httpapi

import (
	"strings"
	"time"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// taskUpdateInput is the Go-side task state contract shared by the HTTP worker
// heartbeat endpoint and the Go-owned upstream executor.
type taskUpdateInput struct {
	Status             string  `json:"status"`
	Progress           *int    `json:"progress"`
	Message            *string `json:"message"`
	WorkerID           *string `json:"workerId"`
	LeaseToken         *string `json:"leaseToken"`
	UpstreamOrderID    *string `json:"upstreamOrderId"`
	ErrorCode          *string `json:"errorCode"`
	ErrorMessage       *string `json:"errorMessage"`
	RawOutput          *string `json:"rawOutput"`
	FailureScreenshots *string `json:"failureScreenshots"`
	CardLast4          *string `json:"cardLast4"`
	GPTAPIOrderID      *string `json:"gptApiOrderId"`
	GPTAPITaskID       *string `json:"gptApiTaskId"`
	GPTAPIRaw          *string `json:"gptApiRaw"`
	GPTAPITopupCode    *string `json:"gptApiTopupCode"`
	Attempt            *int    `json:"attempt"`
	ClientIP           *string `json:"clientIp"`
}

// applyTaskUpdate keeps task transitions, CDK release/use semantics and
// notifications in the Go service regardless of which worker mode produced
// the update.
func (s *Server) applyTaskUpdate(taskID string, input taskUpdateInput) (string, map[string]string, error) {
	var task models.RechargeTask
	notifyEvent := ""
	notifyPayload := map[string]string{}
	admissionToken := ""
	terminalTransition := false
	leaseOwnedForRefresh := false
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, "id = ?", taskID).Error; err != nil {
			return err
		}
		admissionToken = strings.TrimSpace(task.AdmissionToken)
		previousStatus := task.Status
		wasTerminal := isTerminal(previousStatus)
		workerID := ""
		leaseToken := ""
		if input.WorkerID != nil {
			workerID = strings.TrimSpace(*input.WorkerID)
		}
		if input.LeaseToken != nil {
			leaseToken = strings.TrimSpace(*input.LeaseToken)
		}
		leaseOwned := hasTaskLeaseCredentials(workerID, leaseToken)
		if leaseOwned {
			if wasTerminal {
				return errTaskLeaseLost
			}
			if err := validateTaskLease(&task, workerID, leaseToken, time.Now()); err != nil {
				return err
			}
		}
		if wasTerminal && input.Status != previousStatus {
			return nil
		}

		updates := map[string]any{}
		if input.Status != previousStatus {
			updates["status"] = input.Status
		}
		if input.Progress != nil {
			progress := clampProgress(*input.Progress)
			if progress > task.Progress {
				updates["progress"] = progress
			}
		}
		setOptional := func(column string, value *string, trim bool) {
			if value == nil {
				return
			}
			candidate := *value
			if trim {
				candidate = strings.TrimSpace(candidate)
			}
			if strings.TrimSpace(candidate) != "" {
				updates[column] = candidate
			}
		}
		setOptional("message", input.Message, true)
		setOptional("worker_id", input.WorkerID, true)
		setOptional("upstream_order_id", input.UpstreamOrderID, true)
		setOptional("error_code", input.ErrorCode, true)
		setOptional("error_message", input.ErrorMessage, true)
		setOptional("raw_output", input.RawOutput, false)
		setOptional("failure_screenshots", input.FailureScreenshots, false)
		setOptional("card_last4", input.CardLast4, true)
		setOptional("gpt_api_order_id", input.GPTAPIOrderID, true)
		setOptional("gpt_api_task_id", input.GPTAPITaskID, true)
		setOptional("gpt_api_raw", input.GPTAPIRaw, false)
		setOptional("gpt_api_topup_code", input.GPTAPITopupCode, true)
		clientIP := task.ClientIP
		if input.ClientIP != nil {
			if value := strings.TrimSpace(*input.ClientIP); value != "" {
				clientIP = value
				updates["client_ip"] = value
			}
		}
		if input.Attempt != nil {
			updates["attempt"] = *input.Attempt
		}
		now := time.Now()
		if input.Status == models.TaskRunning && task.StartedAt == nil {
			updates["started_at"] = now
		}
		terminalTransition = !wasTerminal && isTerminal(input.Status)
		leaseOwnedForRefresh = leaseOwned && !terminalTransition
		if leaseOwned && !terminalTransition {
			updates["heartbeat_at"] = now
			updates["lease_expires_at"] = now.Add(s.taskLeaseTimeout())
		}
		if terminalTransition {
			updates["finished_at"] = now
			updates["worker_lease_token"] = ""
			updates["admission_token"] = ""
			updates["heartbeat_at"] = nil
			updates["lease_expires_at"] = nil
		}
		if len(updates) > 0 {
			if err := tx.Model(&task).Updates(updates).Error; err != nil {
				return err
			}
		}

		message := task.Message
		if input.Message != nil && strings.TrimSpace(*input.Message) != "" {
			message = strings.TrimSpace(*input.Message)
		}
		errorCode := ""
		if input.ErrorCode != nil {
			errorCode = strings.TrimSpace(*input.ErrorCode)
		}
		taskCDKID := ""
		if task.CDKID != nil {
			taskCDKID = strings.TrimSpace(*task.CDKID)
		}
		if terminalTransition && taskCDKID != "" {
			var cdk models.CDK
			if err := tx.First(&cdk, "id = ?", taskCDKID).Error; err != nil {
				return err
			}
			if input.Status == models.TaskSucceeded {
				if err := tx.Model(&cdk).Updates(map[string]any{"status": models.CDKUsed, "used_at": now, "fail_count": 0, "cooldown_until": nil}).Error; err != nil {
					return err
				}
				if err := resetAttemptFailureTx(tx, "ip", clientIP); err != nil {
					return err
				}
			} else if input.Status == models.TaskFailed || input.Status == models.TaskManual {
				if err := tx.Model(&cdk).Updates(map[string]any{"status": models.CDKAvailable, "used_by_task_id": "", "used_at": nil}).Error; err != nil {
					return err
				}
				if errorCode == "not_eligible" {
					failCount := cdk.FailCount + 1
					cdkUpdates := map[string]any{"fail_count": failCount}
					cooldownParts := []string{}
					if failCount >= 3 {
						cdkUpdates["fail_count"] = 0
						cdkUpdates["cooldown_until"] = now.Add(10 * time.Minute)
						cooldownParts = append(cooldownParts, "该 CDK 已冷却 10 分钟")
					}
					if err := tx.Model(&cdk).Updates(cdkUpdates).Error; err != nil {
						return err
					}
					ipCooled, err := recordAttemptFailureTx(tx, "ip", clientIP, now)
					if err != nil {
						return err
					}
					if ipCooled {
						cooldownParts = append(cooldownParts, "当前 IP 已冷却 10 分钟")
					}
					if len(cooldownParts) > 0 {
						if strings.TrimSpace(message) == "" {
							message = "该账号不符合订阅条件"
						}
						message += "（" + strings.Join(cooldownParts, "，") + "）"
						if err := tx.Model(&task).Update("message", message).Error; err != nil {
							return err
						}
					}
				}
			}
		}
		if terminalTransition {
			notifyEvent = taskNotificationEvent(input.Status, errorCode, message)
			if notifyEvent != "" {
				notifyPayload = map[string]string{
					"cdk":     firstNonEmpty(task.CDKCode, task.CDK.Code),
					"job_key": task.JobKey,
					"message": message,
					"ip":      clientIP,
				}
				if secret, decryptErr := s.decryptSessionValue(task.SessionCiphertext); decryptErr == nil {
					notifyPayload["email"] = subscriptionEmailFromRaw(secret, "")
				}
			}
		}
		return nil
	})
	if err == nil {
		if terminalTransition {
			s.releaseAdmission(admissionToken)
		} else if leaseOwnedForRefresh {
			s.refreshAdmission(admissionToken)
		}
		if input.Status == models.TaskSucceeded {
			go s.queueRedeemEmail(taskID, task.TraceID)
		}
	}
	return notifyEvent, notifyPayload, err
}
