package bakapy

import (
	"fmt"
	"github.com/op/go-logging"
	"log/syslog"
	"net/smtp"
	"os"
	"os/user"
	"path"
	"strings"
)

func SetupLogging(logLevel string) error {
	format := "%{level:.8s} %{module} %{message}"
	stderrBackend := logging.NewLogBackend(os.Stderr, "", 0)
	syslogBackend, err := logging.NewSyslogBackendPriority("", syslog.LOG_CRIT|syslog.LOG_DAEMON)
	if err != nil {
		return err
	}

	logging.SetBackend(stderrBackend, syslogBackend)
	logging.SetFormatter(logging.MustStringFormatter(format))
	level, err := logging.LogLevel(strings.ToUpper(logLevel))
	if err != nil {
		return err
	}
	logging.SetLevel(level, "")
	return nil
}

type NotificationTemplateContext struct {
	From    string
	To      string
	Subject string
	JobName string
	Message string
	Output  string
	Errput  string
}

func SendFailedJobNotification(cfg SMTPConfig, meta *JobMetadata) error {
	if cfg.MailTo == "" {
		curUser, err := user.Current()
		if err != nil {
			curUser = &user.User{"0", "0", "root", "root", "/root"}
		}
		cfg.MailTo = curUser.Name
	}

	if cfg.Host == "" {
		hostname, err := os.Hostname()
		if err != nil {
			cfg.Host = "localhost"
		} else {
			cfg.Host = hostname
		}
	}

	if cfg.MailFrom == "" {
		cfg.MailFrom = fmt.Sprintf("bakapy@%s", cfg.Host)
	}

	if cfg.Port == 0 {
		cfg.Port = 25
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	conn, err := smtp.Dial(addr)
	if err != nil {
		return err
	}

	err = conn.Mail(cfg.MailFrom)
	if err != nil {
		return err
	}

	err = conn.Rcpt(cfg.MailTo)
	if err != nil {
		return err
	}

	ds, err := conn.Data()
	if err != nil {
		return err
	}

	err = MAIL_TEMPLATE_JOB_FAILED.Execute(ds, NotificationTemplateContext{
		From:    cfg.MailFrom,
		To:      cfg.MailTo,
		Subject: fmt.Sprintf("[bakapy] job '%s' failed", meta.JobName),
		Message: meta.Message,
		Output:  string(meta.Output),
		Errput:  string(meta.Errput),
	})
	if err != nil {
		return err
	}

	err = ds.Close()
	if err != nil {
		return err
	}

	err = conn.Quit()
	if err != nil {
		return err
	}
	return nil
}

func RunJob(jobName string, jConfig *JobConfig, gConfig *Config, storage *Storage) string {
	logger := logging.MustGetLogger("bakapy.job")
	executor := jConfig.executor
	if executor == nil {
		executor = NewBashExecutor(jConfig.Args, jConfig.Host, jConfig.Port, jConfig.Sudo)
	}
	job := NewJob(
		jobName, jConfig, gConfig.Listen,
		gConfig.CommandDir, storage, executor,
	)
	metadata := job.Run()
	saveTo := path.Join(gConfig.MetadataDir, string(metadata.TaskId))
	err := metadata.Save(saveTo)
	if err != nil {
		logger.Critical("cannot save metadata: %s", err)
	}
	logger.Info("metadata for job %s successfully saved to %s", metadata.TaskId, saveTo)
	if !metadata.Success {
		logger.Debug("sending failed job notification to current user")
		if err := SendFailedJobNotification(gConfig.SMTP, metadata); err != nil {
			logger.Critical("cannot send failed job notification: %s", err.Error())
		}

		logger.Critical("job '%s' failed", job.Name)
	} else {
		logger.Info("job '%s' finished", job.Name)
	}
	return saveTo
}
