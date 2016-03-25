program UniEngine;

uses
  System.StartUpCopy,
  FMX.Forms,
  Form_Main in 'Form_Main.pas' {FormMain},
  Dialog_EditCnfg in 'Source\Forms\Dialog\Dialog_EditCnfg.pas' {DialogEditCnfg},
  Dialog_ListCnfg in 'Source\Forms\Dialog\Dialog_ListCnfg.pas' {DialogListCnfg};

{$R *.res}

begin
  Application.Initialize;
  Application.CreateForm(TFormMain, FormMain);
  Application.CreateForm(TDialogEditCnfg, DialogEditCnfg);
  Application.CreateForm(TDialogListCnfg, DialogListCnfg);
  Application.Run;
end.
