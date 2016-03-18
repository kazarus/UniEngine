program UniEngine;

uses
  System.StartUpCopy,
  FMX.Forms,
  Form_Main in 'Form_Main.pas' {FormMain},
  Dialog_EditCnfg in 'Source\Forms\Dialog\Dialog_EditCnfg.pas' {Form1};

{$R *.res}

begin
  Application.Initialize;
  Application.CreateForm(TFormMain, FormMain);
  Application.CreateForm(TForm1, Form1);
  Application.Run;
end.
