program UniEngine;

uses
  System.StartUpCopy,
  FMX.Forms,
  Form_Main in 'Form_Main.pas' {FormMain},
  Dialog_EditUniConfig in 'Source\Forms\Dialog\Dialog_EditUniConfig.pas' {DialogEditCnfg},
  Dialog_ListUniConfig in 'Source\Forms\Dialog\Dialog_ListUniConfig.pas' {DialogListUniConfig};

{$R *.res}

begin
  Application.Initialize;
  Application.CreateForm(TFormMain, FormMain);
  Application.CreateForm(TDialogEditCnfg, DialogEditCnfg);
  Application.CreateForm(TDialogListUniConfig, DialogListUniConfig);
  Application.Run;
end.
