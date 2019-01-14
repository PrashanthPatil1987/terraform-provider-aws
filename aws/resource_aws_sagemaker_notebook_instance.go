package aws

import (
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/sagemaker"
	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
)

func resourceAwsSagemakerNotebookInstance() *schema.Resource {
	return &schema.Resource{
		Create: resourceAwsSagemakerNotebookInstanceCreate,
		Read:   resourceAwsSagemakerNotebookInstanceRead,
		Update: resourceAwsSagemakerNotebookInstanceUpdate,
		Delete: resourceAwsSagemakerNotebookInstanceDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"arn": {
				Type:     schema.TypeString,
				Computed: true,
			},

			"name": {
				Type:         schema.TypeString,
				Required:     true,
				Computed:     false,
				ForceNew:     true,
				ValidateFunc: validateSagemakerName,
			},

			"role_arn": {
				Type:     schema.TypeString,
				Required: true,
				Computed: false,
			},

			"instance_type": {
				Type:     schema.TypeString,
				Required: true,
			},

			"subnet_id": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},

			"security_groups": {
				Type:     schema.TypeSet,
				MinItems: 1,
				Optional: true,
				Computed: true,
				ForceNew: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Set:      schema.HashString,
			},

			"kms_key_id": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},

			"tags": tagsSchema(),
		},
	}
}

func resourceAwsSagemakerNotebookInstanceCreate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).sagemakerconn

	name := d.Get("name").(string)

	createOpts := &sagemaker.CreateNotebookInstanceInput{
		SecurityGroupIds:     expandStringSet(d.Get("security_groups").(*schema.Set)),
		NotebookInstanceName: aws.String(name),
		RoleArn:              aws.String(d.Get("role_arn").(string)),
		InstanceType:         aws.String(d.Get("instance_type").(string)),
	}

	if s, ok := d.GetOk("subnet_id"); ok {
		createOpts.SubnetId = aws.String(s.(string))
	}

	if k, ok := d.GetOk("kms_key_id"); ok {
		createOpts.KmsKeyId = aws.String(k.(string))
	}

	if v, ok := d.GetOk("tags"); ok {
		tagsIn := v.(map[string]interface{})
		createOpts.Tags = tagsFromMapSagemaker(tagsIn)
	}

	log.Printf("[DEBUG] Sagemaker Notebook Instance create config: %#v", *createOpts)
	_, err := conn.CreateNotebookInstance(createOpts)
	if err != nil {
		return fmt.Errorf("Error creating Sagemaker Notebook Instance: %s", err)
	}

	d.SetId(name)
	log.Printf("[INFO] Sagemaker Notebook Instance ID: %s", d.Id())

	if err := waitForSagemakerNotebookInstanceStatusInService(conn, d.Id()); err != nil {
		return fmt.Errorf("error waiting for Sagemaker Notebook Instance %q to create: %s", d.Id(), err)
	}

	return resourceAwsSagemakerNotebookInstanceRead(d, meta)
}

func resourceAwsSagemakerNotebookInstanceRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).sagemakerconn

	// TODO change this to describeNotebook Instance
	notebookInstanceRaw, _, err := sagemakerNotebookInstanceStateRefreshFunc(conn, d.Id())()
	if err != nil {
		if awserr, ok := err.(awserr.Error); ok {
			if awserr.Code() == "ResourceNotFoundException" {
				d.SetId("")
				log.Printf("[LOG] Unable to find SageMaker notebook instance %q; removing from state file", d.Id())
				return nil
			}
		}
		return err
	}

	if notebookInstanceRaw == nil {
		d.SetId("")
		log.Printf("[LOG] Unable to find SageMaker notebook instance %q; removing from state file", d.Id())
		return nil
	}

	notebookInstance := notebookInstanceRaw.(*sagemaker.DescribeNotebookInstanceOutput)

	d.Set("security_groups", flattenStringList(notebookInstance.SecurityGroups))
	if err := d.Set("name", notebookInstance.NotebookInstanceName); err != nil {
		return fmt.Errorf("error setting name for notebook instance %q: %s", d.Id(), err)
	}
	if err := d.Set("role_arn", notebookInstance.RoleArn); err != nil {
		return fmt.Errorf("error setting role_arn for notebook instance %q: %s", d.Id(), err)
	}
	if err := d.Set("instance_type", notebookInstance.InstanceType); err != nil {
		return fmt.Errorf("error setting instance_type for notebook instance %q: %s", d.Id(), err)
	}
	if err := d.Set("subnet_id", notebookInstance.SubnetId); err != nil {
		return fmt.Errorf("error setting subnet_id for notebook instance %q: %s", d.Id(), err)
	}

	if err := d.Set("kms_key_id", notebookInstance.KmsKeyId); err != nil {
		return fmt.Errorf("error setting kms_key_id for notebook instance %q: %s", d.Id(), err)
	}

	if err := d.Set("arn", notebookInstance.NotebookInstanceArn); err != nil {
		return fmt.Errorf("error setting arn for notebook instance %q: %s", d.Id(), err)
	}
	// d.Set("tags", tagsToMap()) TODO tags SageMaker functions
	tagsOutput, err := conn.ListTags(&sagemaker.ListTagsInput{
		ResourceArn: notebookInstance.NotebookInstanceArn,
	})
	if err != nil {
		log.Printf("[ERR] Error reading tags: %s", err)
		return err
	}

	if err := d.Set("tags", tagsToMapSagemaker(tagsOutput.Tags)); err != nil {
		return fmt.Errorf("error setting tags for notebook instance %q: %s", d.Id(), err)
	}
	return nil
}

func resourceAwsSagemakerNotebookInstanceUpdate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).sagemakerconn

	d.Partial(true)

	if err := setSagemakerTags(conn, d); err != nil {
		return err
	}
	d.SetPartial("tags")

	hasChanged := false
	// Update
	updateOpts := &sagemaker.UpdateNotebookInstanceInput{
		NotebookInstanceName: aws.String(d.Get("name").(string)),
	}

	if d.HasChange("role_arn") {
		updateOpts.RoleArn = aws.String(d.Get("role_arn").(string))
		hasChanged = true
	}

	if d.HasChange("instance_type") {
		updateOpts.InstanceType = aws.String(d.Get("instance_type").(string))
		hasChanged = true
	}

	if hasChanged {

		// Stop notebook
		_, previousStatus, _ := sagemakerNotebookInstanceStateRefreshFunc(conn, d.Id())()
		if err := stopSagemakerNotebookInstance(conn, d.Id()); err != nil {
			return fmt.Errorf("error stopping Sagemaker Notebook Instance: %s", err)
		}

		if _, err := conn.UpdateNotebookInstance(updateOpts); err != nil {
			return fmt.Errorf("error updating Sagemaker Notebook Instance: %s", err)
		}

		stateConf := &resource.StateChangeConf{
			Pending: []string{
				sagemaker.NotebookInstanceStatusUpdating,
			},
			Target:  []string{sagemaker.NotebookInstanceStatusStopped},
			Refresh: sagemakerNotebookInstanceStateRefreshFunc(conn, d.Id()),
			Timeout: 10 * time.Minute,
		}
		_, err := stateConf.WaitForState()
		if err != nil {
			return fmt.Errorf("error waiting for Sagemaker Notebook Instance %q to update: %s", d.Id(), err)
		}

		// Restart if needed
		if previousStatus == sagemaker.NotebookInstanceStatusInService {
			startOpts := &sagemaker.StartNotebookInstanceInput{
				NotebookInstanceName: aws.String(d.Id()),
			}

			// StartNotebookInstance sometimes doesn't take so we'll check for a state change and if
			// it doesn't change we'll send another request
			err := resource.Retry(5*time.Minute, func() *resource.RetryError {
				if _, err := conn.StartNotebookInstance(startOpts); err != nil {
					return resource.NonRetryableError(fmt.Errorf("error starting Sagemaker Notebook Instance %q: %s", d.Id(), err))
				}
				stateConf := &resource.StateChangeConf{
					Pending: []string{
						sagemaker.NotebookInstanceStatusStopped,
					},
					Target:  []string{sagemaker.NotebookInstanceStatusInService, sagemaker.NotebookInstanceStatusPending},
					Refresh: sagemakerNotebookInstanceStateRefreshFunc(conn, d.Id()),
					Timeout: 30 * time.Second,
				}
				_, err := stateConf.WaitForState()
				if err != nil {
					return resource.RetryableError(fmt.Errorf("error waiting for Sagemaker Notebook Instance %q to start: %s", d.Id(), err))
				}

				return nil
			})

			if err := waitForSagemakerNotebookInstanceStatusInService(conn, d.Id()); err != nil {
				return fmt.Errorf("error waiting for Sagemaker Notebook Instance %q to start after update: %s", d.Id(), err)
			}

			if err != nil {
				return err
			}
		}
	}

	d.Partial(false)

	return resourceAwsSagemakerNotebookInstanceRead(d, meta)
}

func resourceAwsSagemakerNotebookInstanceDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).sagemakerconn

	describeNotebookInput := &sagemaker.DescribeNotebookInstanceInput{
		NotebookInstanceName: aws.String(d.Id()),
	}
	notebook, err := conn.DescribeNotebookInstance(describeNotebookInput)
	if err != nil {
		if sagemakerErr, ok := err.(awserr.Error); ok && sagemakerErr.Code() == "ResourceNotFound" {
			return nil
		}
		return fmt.Errorf("unable to find sagemaker notebook instance to delete %q: %s", d.Id(), err)
	}
	if *notebook.NotebookInstanceStatus != sagemaker.NotebookInstanceStatusFailed {
		if err := stopSagemakerNotebookInstance(conn, d.Id()); err != nil {
			return err
		}
	}

	deleteOpts := &sagemaker.DeleteNotebookInstanceInput{
		NotebookInstanceName: aws.String(d.Id()),
	}

	if _, err := conn.DeleteNotebookInstance(deleteOpts); err != nil {
		return fmt.Errorf("error trying to delete Sagemaker Notebook Instance %q: %s", d.Id(), err)
	}

	if err := waitForSagemakerNotebookInstanceStatusDeleted(conn, d.Id()); err != nil {
		return fmt.Errorf("error waiting for Sagemaker Notebook Instance %q to delete: %s", d.Id(), err)
	}

	return nil
}

func sagemakerNotebookInstanceStateRefreshFunc(conn *sagemaker.SageMaker, name string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		describeNotebookInput := &sagemaker.DescribeNotebookInstanceInput{
			NotebookInstanceName: aws.String(name),
		}
		notebook, err := conn.DescribeNotebookInstance(describeNotebookInput)
		if err != nil {
			if sagemakerErr, ok := err.(awserr.Error); ok && sagemakerErr.Message() == "RecordNotFound" {
				return 1, "", nil
			}
		}

		if notebook == nil {
			return nil, "", nil
		}

		return notebook, *notebook.NotebookInstanceStatus, nil
	}
}

func stopSagemakerNotebookInstance(conn *sagemaker.SageMaker, id string) error {
	describeNotebookInput := &sagemaker.DescribeNotebookInstanceInput{
		NotebookInstanceName: aws.String(id),
	}
	notebook, err := conn.DescribeNotebookInstance(describeNotebookInput)
	if err != nil {
		if sagemakerErr, ok := err.(awserr.Error); ok && sagemakerErr.Code() == "ResourceNotFound" {
			return nil
		}
		return fmt.Errorf("unable to find sagemaker notebook instance %q: %s", id, err)
	}
	if *notebook.NotebookInstanceStatus == sagemaker.NotebookInstanceStatusStopped {
		return nil
	}

	stopOpts := &sagemaker.StopNotebookInstanceInput{
		NotebookInstanceName: aws.String(id),
	}

	if _, err := conn.StopNotebookInstance(stopOpts); err != nil {
		return fmt.Errorf("Error stopping Sagemaker Notebook Instance: %s", err)
	}

	return waitForSagemakerNotebookInstanceStatusStopped(conn, id)
}

func waitForSagemakerNotebookInstanceStatusInService(conn *sagemaker.SageMaker, id string) error {
	stateConf := &resource.StateChangeConf{
		Pending: []string{
			sagemaker.NotebookInstanceStatusUpdating,
			sagemaker.NotebookInstanceStatusPending,
			sagemaker.NotebookInstanceStatusStopped,
		},
		Target:  []string{sagemaker.NotebookInstanceStatusInService},
		Refresh: sagemakerNotebookInstanceStateRefreshFunc(conn, id),
		Timeout: 10 * time.Minute,
	}
	_, err := stateConf.WaitForState()

	return err
}

func waitForSagemakerNotebookInstanceStatusStopped(conn *sagemaker.SageMaker, id string) error {
	stateConf := &resource.StateChangeConf{
		Pending: []string{
			sagemaker.NotebookInstanceStatusStopping,
		},
		Target:  []string{sagemaker.NotebookInstanceStatusStopped},
		Refresh: sagemakerNotebookInstanceStateRefreshFunc(conn, id),
		Timeout: 10 * time.Minute,
	}
	_, err := stateConf.WaitForState()

	return err
}

func waitForSagemakerNotebookInstanceStatusDeleted(conn *sagemaker.SageMaker, id string) error {
	stateConf := &resource.StateChangeConf{
		Pending: []string{
			sagemaker.NotebookInstanceStatusDeleting,
		},
		Target:  []string{""},
		Refresh: sagemakerNotebookInstanceStateRefreshFunc(conn, id),
		Timeout: 10 * time.Minute,
	}
	_, err := stateConf.WaitForState()

	return err
}
