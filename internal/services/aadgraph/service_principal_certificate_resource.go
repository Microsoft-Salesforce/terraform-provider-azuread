package aadgraph

import (
	"fmt"
	"log"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/graphrbac/1.6/graphrbac"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"

	"github.com/terraform-providers/terraform-provider-azuread/internal/clients"
	"github.com/terraform-providers/terraform-provider-azuread/internal/services/aadgraph/graph"
	"github.com/terraform-providers/terraform-provider-azuread/internal/tf"
	"github.com/terraform-providers/terraform-provider-azuread/internal/utils"
)

func servicePrincipalCertificateResource() *schema.Resource {
	return &schema.Resource{
		Create: servicePrincipalCertificateResourceCreate,
		Read:   servicePrincipalCertificateResourceRead,
		Delete: servicePrincipalCertificateResourceDelete,

		Importer: tf.ValidateResourceIDPriorToImport(func(id string) error {
			_, err := graph.ParseCertificateId(id)
			return err
		}),

		Schema: graph.CertificateResourceSchema("service_principal_id"),
	}
}

func servicePrincipalCertificateResourceCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.AadClient).AadGraph.ServicePrincipalsClient
	ctx := meta.(*clients.AadClient).StopContext

	objectId := d.Get("service_principal_id").(string)

	cred, err := graph.KeyCredentialForResource(d)
	if err != nil {
		return fmt.Errorf("generating certificate credentials for object ID %q: %+v", objectId, err)
	}
	id := graph.CredentialIdFrom(objectId, "certificate", *cred.KeyID)

	tf.LockByName(servicePrincipalResourceName, id.ObjectId)
	defer tf.UnlockByName(servicePrincipalResourceName, id.ObjectId)

	existingCreds, err := client.ListKeyCredentials(ctx, id.ObjectId)
	if err != nil {
		return fmt.Errorf("listing certificate credentials for service principal with ID %q: %+v", id.ObjectId, err)
	}

	newCreds, err := graph.KeyCredentialResultAdd(existingCreds, cred)
	if err != nil {
		if _, ok := err.(*graph.AlreadyExistsError); ok {
			return tf.ImportAsExistsError("azuread_service_principal_certificate", id.String())
		}
		return fmt.Errorf("adding Service Principal Certificate: %+v", err)
	}

	if _, err = client.UpdateKeyCredentials(ctx, id.ObjectId, graphrbac.KeyCredentialsUpdateParameters{Value: newCreds}); err != nil {
		return fmt.Errorf("creating certificate credentials %q for service principal with ID %q: %+v", id.KeyId, id.ObjectId, err)
	}

	_, err = graph.WaitForKeyCredentialReplication(id.KeyId, d.Timeout(schema.TimeoutCreate), func() (graphrbac.KeyCredentialListResult, error) {
		return client.ListKeyCredentials(ctx, id.ObjectId)
	})
	if err != nil {
		return fmt.Errorf("waiting for certificate credential replication for service principal (ID %q, KeyID %q: %+v", id.ObjectId, id.KeyId, err)
	}

	d.SetId(id.String())

	return servicePrincipalCertificateResourceRead(d, meta)
}

func servicePrincipalCertificateResourceRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.AadClient).AadGraph.ServicePrincipalsClient
	ctx := meta.(*clients.AadClient).StopContext

	id, err := graph.ParseCertificateId(d.Id())
	if err != nil {
		return fmt.Errorf("parsing certificate credential with ID: %v", err)
	}
	// ensure the Service Principal Object exists
	sp, err := client.Get(ctx, id.ObjectId)
	if err != nil {
		// the parent Service Principal has been removed - skip it
		if utils.ResponseWasNotFound(sp.Response) {
			log.Printf("[DEBUG] Service Principal with Object ID %q was not found - removing from state!", id.ObjectId)
			d.SetId("")
			return nil
		}
		return fmt.Errorf("retrieving service principal with ID %q: %+v", id.ObjectId, err)
	}

	credentials, err := client.ListKeyCredentials(ctx, id.ObjectId)
	if err != nil {
		return fmt.Errorf("listing certificate credentials for service principal with ID %q: %+v", id.ObjectId, err)
	}

	credential := graph.KeyCredentialResultFindByKeyId(credentials, id.KeyId)
	if credential == nil {
		log.Printf("[DEBUG] certificate credential %q (ID %q) was not found - removing from state!", id.KeyId, id.ObjectId)
		d.SetId("")
		return nil
	}

	// todo, move this into a graph helper function?
	d.Set("service_principal_id", id.ObjectId)
	d.Set("key_id", id.KeyId)

	if keyType := credential.Type; keyType != nil {
		d.Set("type", keyType)
	}

	if endDate := credential.EndDate; endDate != nil {
		d.Set("end_date", endDate.Format(time.RFC3339))
	}

	if startDate := credential.StartDate; startDate != nil {
		d.Set("start_date", startDate.Format(time.RFC3339))
	}

	return nil
}

func servicePrincipalCertificateResourceDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.AadClient).AadGraph.ServicePrincipalsClient
	ctx := meta.(*clients.AadClient).StopContext

	id, err := graph.ParseCertificateId(d.Id())
	if err != nil {
		return fmt.Errorf("parsing certificate credential with ID: %v", err)
	}

	tf.LockByName(servicePrincipalResourceName, id.ObjectId)
	defer tf.UnlockByName(servicePrincipalResourceName, id.ObjectId)

	// ensure the parent Service Principal exists
	sp, err := client.Get(ctx, id.ObjectId)
	if err != nil {
		// the parent Service Principal has been removed - skip it
		if utils.ResponseWasNotFound(sp.Response) {
			log.Printf("[DEBUG] Service Principal with Object ID %q was not found - removing from state!", id.ObjectId)
			return nil
		}
		return fmt.Errorf("retrieving service principal with ID %q: %+v", id.ObjectId, err)
	}

	existing, err := client.ListKeyCredentials(ctx, id.ObjectId)
	if err != nil {
		return fmt.Errorf("listing certificate credentials for service principal %q: %+v", id.ObjectId, err)
	}

	newCreds := graph.KeyCredentialResultRemoveByKeyId(existing, id.KeyId)
	if _, err = client.UpdateKeyCredentials(ctx, id.ObjectId, graphrbac.KeyCredentialsUpdateParameters{Value: newCreds}); err != nil {
		return fmt.Errorf("removing certificate credentials %q from service principal with ID %q: %+v", id.KeyId, id.ObjectId, err)
	}

	return nil
}
